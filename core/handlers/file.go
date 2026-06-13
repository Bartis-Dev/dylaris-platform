package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	pb "dylaris-proto/node"

	"github.com/google/uuid"
)

// FileInfo is a struct to hold file and directory information
type FileInfo struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// FileHandler is the handler for file-related API calls
type FileHandler struct {
	RootPath string
	state    *AppState
}

// Regex for sanitizing filenames
var validFilenameRegex = regexp.MustCompile(`[^a-zA-Z0-9._\-+]`)

func sanitizeFilename(name string) string {
	return validFilenameRegex.ReplaceAllString(name, "_")
}

func NewFileHandler(state *AppState) *FileHandler {
	return &FileHandler{RootPath: "servers", state: state}
}

// getTransferLimit returns the upload or download limit in bytes for the current user
func (h *FileHandler) getTransferLimit(r *http.Request, limitType string) int64 {
	isAdmin := IsAdmin(r)
	var key string
	var defaultVal int64
	if limitType == "upload" {
		if isAdmin {
			key = "fm.admin_upload_limit"
			defaultVal = 2 * 1024 * 1024 * 1024
		} else {
			key = "fm.user_upload_limit"
			defaultVal = 500 * 1024 * 1024
		}
	} else {
		if isAdmin {
			key = "fm.admin_download_limit"
			defaultVal = 5 * 1024 * 1024 * 1024
		} else {
			key = "fm.user_download_limit"
			defaultVal = 1 * 1024 * 1024 * 1024
		}
	}
	val, err := h.state.Store.GetSetting(key)
	if err != nil || val == "" {
		return defaultVal
	}
	var n int64
	if _, err := fmt.Sscanf(val, "%d", &n); err == nil && n > 0 {
		return n
	}
	return defaultVal
}

// getServerUUID extracts and validates the server_uuid query param.
// Non-admin users must own the server with that UUID.
func (h *FileHandler) getServerUUID(r *http.Request) (string, error) {
	uuid := r.URL.Query().Get("server_uuid")
	if uuid == "" {
		uuid = r.FormValue("server_uuid")
	}
	if uuid == "" {
		return "", nil
	}
	if h.state == nil || h.state.Store == nil {
		return uuid, nil
	}
	username, _ := r.Context().Value("username").(string)
	isAdmin, _ := r.Context().Value("isAdmin").(bool)
	userID, _ := r.Context().Value("userID").(string)
	if isAdmin {
		return uuid, nil
	}
	srv, err := h.state.Store.GetServerByUUID(uuid)
	if err != nil {
		return "", fmt.Errorf("server not found")
	}
	// Honor invited-member permissions, not just ownership: a user invited to
	// this server with the "files" permission may use the file browser.
	if !checkServerAccess(h.state.Store, srv, username, isAdmin, userID, "files") {
		return "", fmt.Errorf("access denied")
	}
	return uuid, nil
}

// getNodeIDForServer looks up which Node owns this server and returns its NodeID.
func (h *FileHandler) getNodeIDForServer(serverUUID string) (int, error) {
	srv, err := h.state.Store.GetServerByUUID(serverUUID)
	if err != nil {
		return 0, fmt.Errorf("server not found: %w", err)
	}
	return srv.NodeID, nil
}

// sendGRPCMsg sends a pre-built NodeMessage via gRPC and waits for a response.
func (h *FileHandler) sendGRPCMsg(nodeID int, msg *pb.NodeMessage, timeout time.Duration) (*pb.NodeMessage, error) {
	if h.state.GRPCRegistry == nil {
		return nil, fmt.Errorf("gRPC registry not initialized")
	}
	return h.state.GRPCRegistry.SendRequest(nodeID, msg, timeout)
}

// GetFilesHandler handles requests to list files in a directory
func (h *FileHandler) GetFilesHandler(w http.ResponseWriter, r *http.Request) {
	pathParam := r.URL.Query().Get("path")
	if pathParam == "" {
		pathParam = "/"
	}
	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	resp, err := h.sendGRPCMsg(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: serverUUID,
		Payload:    &pb.NodeMessage_ListReq{ListReq: &pb.ListFilesReq{Path: pathParam}},
	}, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}

	// Check for error response
	if errResp := resp.GetError(); errResp != nil {
		if errResp.Code == 403 {
			sendJSONError(w, errResp.Message, http.StatusForbidden)
		} else {
			sendJSONError(w, errResp.Message, int(errResp.Code))
		}
		return
	}

	listResp := resp.GetListResp()
	if listResp == nil {
		sendJSONError(w, "unexpected response from node", http.StatusInternalServerError)
		return
	}

	fileList := make([]FileInfo, 0, len(listResp.Files))
	for _, f := range listResp.Files {
		fileList = append(fileList, FileInfo{
			Name:  f.Name,
			IsDir: f.IsDir,
			Size:  f.Size,
		})
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"files":   fileList,
	})
}

// GetFileContentHandler handles requests to read the content of a file
func (h *FileHandler) GetFileContentHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	// Use streaming request to receive file chunks
	reqID := uuid.NewString()
	msg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_ReadReq{
			ReadReq: &pb.ReadFileReq{Path: path, ZipIfDir: false},
		},
	}

	ch, err := h.state.GRPCRegistry.SendRequestStreaming(nodeID, msg)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	defer h.state.GRPCRegistry.CleanupRequest(nodeID, reqID)

	// Collect all chunks into a buffer
	var buf bytes.Buffer
	for resp := range ch {
		if errResp := resp.GetError(); errResp != nil {
			sendJSONError(w, errResp.Message, int(errResp.Code))
			return
		}
		if chunk := resp.GetChunk(); chunk != nil {
			buf.Write(chunk.Data)
		}
		// TransferDone signals end — channel will be closed by registry
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"content": buf.String(),
	})
}

// SaveFileHandler handles requests to save file content
func (h *FileHandler) SaveFileHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	conn, ok := h.state.GRPCRegistry.GetConnection(nodeID)
	if !ok {
		sendJSONError(w, fmt.Sprintf("Node %d not connected", nodeID), http.StatusBadGateway)
		return
	}

	reqID := uuid.NewString()
	data := []byte(req.Content)

	// Step 1: Send WriteFileReq
	writeReqMsg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_WriteReq{
			WriteReq: &pb.WriteFileReq{Path: req.Path, TotalSize: int64(len(data))},
		},
	}

	resp, err := h.state.GRPCRegistry.SendRequest(nodeID, writeReqMsg, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, int(errResp.Code))
		return
	}

	// Step 2: Send data chunks
	const chunkSize = 64 * 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunkMsg := &pb.NodeMessage{
			RequestId:  reqID,
			ServerUuid: serverUUID,
			Payload: &pb.NodeMessage_Chunk{
				Chunk: &pb.DataChunk{Data: data[offset:end], Offset: int64(offset)},
			},
		}
		if err := conn.Stream.Send(chunkMsg); err != nil {
			sendJSONError(w, fmt.Sprintf("Failed to send chunk: %v", err), http.StatusBadGateway)
			return
		}
	}

	// Step 3: Send TransferDone
	doneMsg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{TotalBytes: int64(len(data))},
		},
	}
	if err := conn.Stream.Send(doneMsg); err != nil {
		sendJSONError(w, fmt.Sprintf("Failed to send transfer done: %v", err), http.StatusBadGateway)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "File saved successfully",
	})
}

// CreateFileHandler handles requests to create a new file or directory
func (h *FileHandler) CreateFileHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	dir, file := filepath.Split(req.Path)
	cleanFile := sanitizeFilename(file)
	req.Path = filepath.Join(dir, cleanFile)

	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	resp, err := h.sendGRPCMsg(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: serverUUID,
		Payload:    &pb.NodeMessage_CreateReq{CreateReq: &pb.CreateFileReq{Path: req.Path, IsDir: req.IsDir}},
	}, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, int(errResp.Code))
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// RenameFileHandler handles renaming files and directories
func (h *FileHandler) RenameFileHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	_, newFile := filepath.Split(req.NewPath)
	cleanFile := sanitizeFilename(newFile)

	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	resp, err := h.sendGRPCMsg(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: serverUUID,
		Payload:    &pb.NodeMessage_RenameReq{RenameReq: &pb.RenameFileReq{OldPath: req.OldPath, NewName: cleanFile}},
	}, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, int(errResp.Code))
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// CopyFileHandler handles copying files and directories
func (h *FileHandler) CopyFileHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	resp, err := h.sendGRPCMsg(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: serverUUID,
		Payload:    &pb.NodeMessage_CopyReq{CopyReq: &pb.CopyFileReq{SrcPath: req.OldPath, DstPath: req.NewPath}},
	}, 60*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, int(errResp.Code))
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DeleteFileHandler handles requests to delete a file
func (h *FileHandler) DeleteFileHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	resp, err := h.sendGRPCMsg(nodeID, &pb.NodeMessage{
		RequestId:  uuid.NewString(),
		ServerUuid: serverUUID,
		Payload:    &pb.NodeMessage_DeleteReq{DeleteReq: &pb.DeleteFileReq{Path: req.Path}},
	}, 30*time.Second)
	if err != nil {
		sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	if errResp := resp.GetError(); errResp != nil {
		sendJSONError(w, errResp.Message, int(errResp.Code))
		return
	}

	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// DownloadFileHandler handles file and folder downloads via gRPC streaming
func (h *FileHandler) DownloadFileHandler(w http.ResponseWriter, r *http.Request) {
	pathParam := r.URL.Query().Get("path")
	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		http.Error(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Request file/dir read with zip_if_dir=true for directories
	reqID := uuid.NewString()
	msg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_ReadReq{
			ReadReq: &pb.ReadFileReq{Path: pathParam, ZipIfDir: true},
		},
	}

	ch, err := h.state.GRPCRegistry.SendRequestStreaming(nodeID, msg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	defer h.state.GRPCRegistry.CleanupRequest(nodeID, reqID)

	// Stream chunks to HTTP response.
	// First message is metadata TransferDone (TotalBytes=0) with filename — set headers.
	// Then stream data chunks directly to browser (zero-copy).
	// Final TransferDone (TotalBytes>0) signals completion — channel closes.
	headerWritten := false
	flusher, canFlush := w.(http.Flusher)

	for resp := range ch {
		if errResp := resp.GetError(); errResp != nil {
			if !headerWritten {
				http.Error(w, errResp.Message, int(errResp.Code))
			}
			return
		}

		// Metadata TransferDone (TotalBytes=0): set headers before any data
		if done := resp.GetTransferDone(); done != nil && done.TotalBytes == 0 {
			w.Header().Set("Content-Type", "application/octet-stream")
			if done.Filename != "" {
				w.Header().Set("Content-Disposition", "attachment; filename=\""+done.Filename+"\"")
			}
			headerWritten = true
			continue
		}

		if chunk := resp.GetChunk(); chunk != nil {
			if !headerWritten {
				w.Header().Set("Content-Type", "application/octet-stream")
				headerWritten = true
			}
			w.Write(chunk.Data)
			if canFlush {
				flusher.Flush()
			}
		}

		// Final TransferDone (TotalBytes>0): transfer complete, channel will close
	}
}

// SelectiveDownloadHandler handles selective folder downloads.
// GET /api/files/download/selective?server_uuid=...&base_path=...&selected=a&selected=b&select_all=false
func (h *FileHandler) SelectiveDownloadHandler(w http.ResponseWriter, r *http.Request) {
	basePath := r.URL.Query().Get("base_path")
	selectedPaths := r.URL.Query()["selected"]
	selectAll := r.URL.Query().Get("select_all") == "true"

	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		http.Error(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	if !selectAll && len(selectedPaths) == 0 {
		http.Error(w, "no paths selected", http.StatusBadRequest)
		return
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	reqID := uuid.NewString()
	msg := &pb.NodeMessage{
		RequestId:  reqID,
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_SelectiveReadReq{
			SelectiveReadReq: &pb.SelectiveReadReq{
				BasePath:  basePath,
				Selected:  selectedPaths,
				SelectAll: selectAll,
			},
		},
	}

	ch, err := h.state.GRPCRegistry.SendRequestStreaming(nodeID, msg)
	if err != nil {
		http.Error(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
		return
	}
	defer h.state.GRPCRegistry.CleanupRequest(nodeID, reqID)

	headerWritten := false
	flusher, canFlush := w.(http.Flusher)

	for resp := range ch {
		if errResp := resp.GetError(); errResp != nil {
			if !headerWritten {
				http.Error(w, errResp.Message, int(errResp.Code))
			}
			return
		}

		if done := resp.GetTransferDone(); done != nil && done.TotalBytes == 0 {
			w.Header().Set("Content-Type", "application/octet-stream")
			if done.Filename != "" {
				w.Header().Set("Content-Disposition", "attachment; filename=\""+done.Filename+"\"")
			}
			headerWritten = true
			continue
		}

		if chunk := resp.GetChunk(); chunk != nil {
			if !headerWritten {
				w.Header().Set("Content-Type", "application/octet-stream")
				headerWritten = true
			}
			w.Write(chunk.Data)
			if canFlush {
				flusher.Flush()
			}
		}
	}
}

// UploadFileHandler handles uploads — receives files via HTTP multipart,
// then streams them to the Node via gRPC chunks.
func (h *FileHandler) UploadFileHandler(w http.ResponseWriter, r *http.Request) {
	uploadLimit := h.getTransferLimit(r, "upload")
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)
	// 32MiB in memory; anything larger spills to a temp file on disk. Passing
	// uploadLimit here would buffer the whole (up to multi-GB) upload in RAM.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		sendJSONError(w, "File is too large or exceeds your upload limit", http.StatusBadRequest)
		return
	}

	path := r.FormValue("path")
	serverUUID, err := h.getServerUUID(r)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusForbidden)
		return
	}
	if serverUUID == "" {
		sendJSONError(w, "server_uuid required", http.StatusBadRequest)
		return
	}

	// Disk quota pre-check: reject upload if it would exceed the limit
	if h.state.Redis != nil {
		diskKey := fmt.Sprintf("dylaris:server:%s:stats:disk", serverUUID)
		if diskData, err := h.state.Redis.Get(context.Background(), diskKey).Result(); err == nil {
			var diskInfo struct {
				Total int64 `json:"total"`
				Limit int64 `json:"limit"`
			}
			if json.Unmarshal([]byte(diskData), &diskInfo) == nil && diskInfo.Limit > 0 {
				// Sum all file sizes from the upload
				var totalUploadSize int64
				files := r.MultipartForm.File["files"]
				for _, fh := range files {
					totalUploadSize += fh.Size
				}
				freeBytes := diskInfo.Limit - diskInfo.Total
				if freeBytes < 0 {
					freeBytes = 0
				}
				if diskInfo.Total+totalUploadSize > diskInfo.Limit {
					sendJSONError(w, fmt.Sprintf(
						"Speicherlimit erreicht — %s frei, Upload ist %s",
						formatBytesHuman(freeBytes),
						formatBytesHuman(totalUploadSize),
					), http.StatusRequestEntityTooLarge)
					return
				}
			}
		}
	}

	nodeID, err := h.getNodeIDForServer(serverUUID)
	if err != nil {
		sendJSONError(w, err.Error(), http.StatusNotFound)
		return
	}

	conn, ok := h.state.GRPCRegistry.GetConnection(nodeID)
	if !ok {
		sendJSONError(w, fmt.Sprintf("Node %d not connected", nodeID), http.StatusBadGateway)
		return
	}

	files := r.MultipartForm.File["files"]
	for _, fileHeader := range files {
		sanitizedName := sanitizeFilename(fileHeader.Filename)
		if sanitizedName == "" || sanitizedName == ".active_server" {
			continue
		}

		file, err := fileHeader.Open()
		if err != nil {
			sendJSONError(w, "Could not open uploaded file", http.StatusInternalServerError)
			return
		}

		filePath := filepath.Join(path, sanitizedName)
		reqID := uuid.NewString()

		// Step 1: WriteFileReq with total size from header
		writeReqMsg := &pb.NodeMessage{
			RequestId:  reqID,
			ServerUuid: serverUUID,
			Payload: &pb.NodeMessage_WriteReq{
				WriteReq: &pb.WriteFileReq{Path: filePath, TotalSize: fileHeader.Size},
			},
		}

		resp, err := h.state.GRPCRegistry.SendRequest(nodeID, writeReqMsg, 30*time.Second)
		if err != nil {
			file.Close()
			sendJSONError(w, fmt.Sprintf("Node communication error: %v", err), http.StatusBadGateway)
			return
		}
		if errResp := resp.GetError(); errResp != nil {
			file.Close()
			sendJSONError(w, errResp.Message, int(errResp.Code))
			return
		}

		// Step 2: Stream chunks directly from multipart file (no RAM buffering)
		const chunkSize = 64 * 1024
		buf := make([]byte, chunkSize)
		var offset int64

		for {
			n, readErr := file.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				chunkMsg := &pb.NodeMessage{
					RequestId:  reqID,
					ServerUuid: serverUUID,
					Payload: &pb.NodeMessage_Chunk{
						Chunk: &pb.DataChunk{Data: chunk, Offset: offset},
					},
				}
				if err := conn.Stream.Send(chunkMsg); err != nil {
					file.Close()
					sendJSONError(w, fmt.Sprintf("Failed to send chunk: %v", err), http.StatusBadGateway)
					return
				}
				offset += int64(n)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				file.Close()
				sendJSONError(w, fmt.Sprintf("Failed to read file: %v", readErr), http.StatusInternalServerError)
				return
			}
		}
		file.Close()

		// Step 3: TransferDone
		doneMsg := &pb.NodeMessage{
			RequestId:  reqID,
			ServerUuid: serverUUID,
			Payload: &pb.NodeMessage_TransferDone{
				TransferDone: &pb.TransferDone{TotalBytes: offset, Filename: sanitizedName},
			},
		}
		if err := conn.Stream.Send(doneMsg); err != nil {
			sendJSONError(w, fmt.Sprintf("Failed to send transfer done: %v", err), http.StatusBadGateway)
			return
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("%d files uploaded successfully", len(files)),
	})
}

// --- Local helpers (used by other handlers, e.g., library) ---

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if err := copyPath(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// formatBytesHuman returns a human-readable byte size (e.g. "1.5 GB", "200 MB").
func formatBytesHuman(b int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func unzip(src, dest string, conflictStrategy string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)

		if !strings.HasPrefix(fpath, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if conflictStrategy == "ignore" {
			if _, err := os.Stat(fpath); err == nil {
				continue
			}
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
