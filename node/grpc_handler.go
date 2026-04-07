package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	pb "dylaris-proto/node"
)

const chunkSize = 64 * 1024 // 64KB

// StreamHandler processes file operation requests from Core via gRPC.
// It operates on the Node's local filesystem.
type StreamHandler struct {
	baseDir    string          // Node's working directory (legacy fallback)
	storageMgr *StorageManager // Multi-path storage manager
}

func NewStreamHandler(storageMgr *StorageManager) *StreamHandler {
	baseDir, _ := os.Getwd()
	return &StreamHandler{baseDir: baseDir, storageMgr: storageMgr}
}

// Handle processes a NodeMessage and returns one or more response messages.
// ReadReq is handled separately via HandleStreaming for memory-efficient streaming.
func (h *StreamHandler) Handle(msg *pb.NodeMessage) []*pb.NodeMessage {
	switch p := msg.Payload.(type) {
	case *pb.NodeMessage_ListReq:
		return []*pb.NodeMessage{h.handleList(msg.RequestId, msg.ServerUuid, p.ListReq)}
	case *pb.NodeMessage_WriteReq:
		return []*pb.NodeMessage{h.handleWrite(msg.RequestId, msg.ServerUuid, p.WriteReq)}
	case *pb.NodeMessage_CreateReq:
		return []*pb.NodeMessage{h.handleCreate(msg.RequestId, msg.ServerUuid, p.CreateReq)}
	case *pb.NodeMessage_DeleteReq:
		return []*pb.NodeMessage{h.handleDelete(msg.RequestId, msg.ServerUuid, p.DeleteReq)}
	case *pb.NodeMessage_RenameReq:
		return []*pb.NodeMessage{h.handleRename(msg.RequestId, msg.ServerUuid, p.RenameReq)}
	case *pb.NodeMessage_CopyReq:
		return []*pb.NodeMessage{h.handleCopy(msg.RequestId, msg.ServerUuid, p.CopyReq)}
	default:
		return []*pb.NodeMessage{errorMsg(msg.RequestId, 400, "unknown request type")}
	}
}

// HandleStreaming processes read operations by streaming chunks via sendFn callback.
// This avoids buffering entire files/zips in memory (~128KB constant RAM per download).
func (h *StreamHandler) HandleStreaming(msg *pb.NodeMessage, sendFn func(*pb.NodeMessage) error) {
	// SelectiveReadReq: zip selected files/folders
	if selReq := msg.GetSelectiveReadReq(); selReq != nil {
		h.streamSelectiveZip(msg.RequestId, msg.ServerUuid, selReq, sendFn)
		return
	}

	readReq := msg.GetReadReq()
	if readReq == nil {
		sendFn(errorMsg(msg.RequestId, 400, "HandleStreaming only supports ReadReq/SelectiveReadReq"))
		return
	}

	filePath, err := h.validatePath(readReq.Path, msg.ServerUuid)
	if err != nil {
		sendFn(errorMsg(msg.RequestId, 403, err.Error()))
		return
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		sendFn(errorMsg(msg.RequestId, 404, "file not found"))
		return
	}

	if stat.IsDir() && readReq.ZipIfDir {
		h.streamDirAsZip(msg.RequestId, filePath, sendFn)
		return
	}

	if stat.IsDir() {
		sendFn(errorMsg(msg.RequestId, 400, "path is a directory, set zip_if_dir=true"))
		return
	}

	h.streamFile(msg.RequestId, filePath, sendFn)
}

// validatePath ensures the path stays within the server's data directory.
func (h *StreamHandler) validatePath(reqPath, serverUUID string) (string, error) {
	if serverUUID == "" {
		return "", fmt.Errorf("server_uuid required")
	}

	// Use StorageManager for dynamic path resolution if available
	var dataPath string
	if h.storageMgr != nil {
		dataPath = h.storageMgr.GetServerDir(serverUUID)
	} else {
		dataPath = filepath.Join(h.baseDir, "dylaris_data", "servers", serverUUID)
	}

	fullPath := filepath.Join(dataPath, reqPath)
	cleanPath := filepath.Clean(fullPath)

	if !strings.HasPrefix(cleanPath, filepath.Clean(dataPath)) {
		return "", fmt.Errorf("access denied: path traversal")
	}
	return cleanPath, nil
}

func (h *StreamHandler) handleList(reqID, serverUUID string, req *pb.ListFilesReq) *pb.NodeMessage {
	dirPath, err := h.validatePath(req.Path, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty list for non-existent directories
			return &pb.NodeMessage{
				RequestId: reqID,
				Payload: &pb.NodeMessage_ListResp{
					ListResp: &pb.ListFilesResp{Files: []*pb.FileInfo{}},
				},
			}
		}
		return errorMsg(reqID, 500, fmt.Sprintf("read dir: %v", err))
	}

	// Pre-build the file list and collect directory entries for concurrent size calculation
	type dirEntry struct {
		info *pb.FileInfo
		path string
	}
	files := make([]*pb.FileInfo, 0, len(entries))
	var dirs []dirEntry
	for _, e := range entries {
		if e.Name() == ".active_server" {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		f := &pb.FileInfo{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  fi.Size(),
		}
		if e.IsDir() {
			dirs = append(dirs, dirEntry{info: f, path: filepath.Join(dirPath, e.Name())})
		}
		files = append(files, f)
	}

	// Calculate directory sizes concurrently
	if len(dirs) > 0 {
		var wg sync.WaitGroup
		for _, de := range dirs {
			wg.Add(1)
			go func(f *pb.FileInfo, p string) {
				defer wg.Done()
				f.Size = dirSize(p)
			}(de.info, de.path)
		}
		wg.Wait()
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_ListResp{
			ListResp: &pb.ListFilesResp{Files: files},
		},
	}
}

// streamFile streams a single file in 64KB chunks via sendFn.
// Sends metadata TransferDone first (TotalBytes=0), then chunks, then final TransferDone.
func (h *StreamHandler) streamFile(reqID, filePath string, sendFn func(*pb.NodeMessage) error) {
	filename := filepath.Base(filePath)

	// Send metadata first (filename for Content-Disposition header)
	if err := sendFn(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{Filename: filename, TotalBytes: 0},
		},
	}); err != nil {
		return
	}

	f, err := os.Open(filePath)
	if err != nil {
		sendFn(errorMsg(reqID, 500, fmt.Sprintf("open: %v", err)))
		return
	}
	defer f.Close()

	buf := make([]byte, chunkSize)
	var offset int64

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := sendFn(&pb.NodeMessage{
				RequestId: reqID,
				Payload: &pb.NodeMessage_Chunk{
					Chunk: &pb.DataChunk{Data: chunk, Offset: offset},
				},
			}); err != nil {
				return
			}
			offset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			sendFn(errorMsg(reqID, 500, fmt.Sprintf("read: %v", readErr)))
			return
		}
	}

	// Final TransferDone — no Filename, so core can distinguish it from the
	// metadata TransferDone even when TotalBytes==0 (empty files).
	sendFn(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{TotalBytes: offset},
		},
	})
}

// streamDirAsZip creates a zip of the directory using io.Pipe and streams chunks
// as they are produced. Constant ~128KB RAM usage regardless of directory size.
func (h *StreamHandler) streamDirAsZip(reqID, dirPath string, sendFn func(*pb.NodeMessage) error) {
	filename := filepath.Base(dirPath) + ".zip"

	// Send metadata first (filename for Content-Disposition header)
	if err := sendFn(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{Filename: filename, TotalBytes: 0},
		},
	}); err != nil {
		return
	}

	pr, pw := io.Pipe()

	// Goroutine: walk directory and write zip data into the pipe
	go func() {
		zw := zip.NewWriter(pw)
		err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			relPath, _ := filepath.Rel(dirPath, path)
			if relPath == "." {
				return nil
			}

			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(relPath)
			if info.IsDir() {
				header.Name += "/"
			} else {
				header.Method = zip.Deflate
			}

			writer, err := zw.CreateHeader(header)
			if err != nil {
				return err
			}

			if !info.IsDir() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				_, err = io.Copy(writer, f)
				return err
			}
			return nil
		})

		zw.Close()
		if err != nil {
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()

	// Main thread: read from pipe in chunks and send immediately
	buf := make([]byte, chunkSize)
	var offset int64

	for {
		n, readErr := pr.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := sendFn(&pb.NodeMessage{
				RequestId: reqID,
				Payload: &pb.NodeMessage_Chunk{
					Chunk: &pb.DataChunk{Data: chunk, Offset: offset},
				},
			}); err != nil {
				pr.Close()
				return
			}
			offset += int64(n)
		}
		if readErr != nil {
			if readErr != io.EOF {
				log.Printf("gRPC: zip streaming error: %v", readErr)
				sendFn(errorMsg(reqID, 500, fmt.Sprintf("zip stream: %v", readErr)))
				return
			}
			break
		}
	}

	// Final TransferDone with TotalBytes > 0 signals completion
	sendFn(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{TotalBytes: offset, Filename: filename},
		},
	})
}

// streamSelectiveZip zips only selected paths from a base directory using io.Pipe streaming.
// If selectAll is true, zips everything (same as streamDirAsZip).
// This is reusable for backup creation (same SelectiveReadReq message).
func (h *StreamHandler) streamSelectiveZip(reqID, serverUUID string, req *pb.SelectiveReadReq, sendFn func(*pb.NodeMessage) error) {
	basePath, err := h.validatePath(req.BasePath, serverUUID)
	if err != nil {
		sendFn(errorMsg(reqID, 403, err.Error()))
		return
	}

	// If select_all, just zip the whole directory
	if req.SelectAll {
		h.streamDirAsZip(reqID, basePath, sendFn)
		return
	}

	filename := "download.zip"
	if base := filepath.Base(basePath); base != "." && base != "/" {
		filename = base + ".zip"
	}

	// Send metadata first
	if err := sendFn(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{Filename: filename, TotalBytes: 0},
		},
	}); err != nil {
		return
	}

	pr, pw := io.Pipe()

	go func() {
		zw := zip.NewWriter(pw)
		var walkErr error

		for _, sel := range req.Selected {
			selPath := filepath.Join(basePath, filepath.Clean(sel))
			// Security: ensure path stays within basePath
			if !strings.HasPrefix(filepath.Clean(selPath), filepath.Clean(basePath)) {
				continue
			}

			stat, err := os.Stat(selPath)
			if err != nil {
				continue
			}

			if stat.IsDir() {
				// Walk entire subdirectory
				walkErr = filepath.Walk(selPath, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					relPath, _ := filepath.Rel(basePath, path)
					header, err := zip.FileInfoHeader(info)
					if err != nil {
						return err
					}
					header.Name = filepath.ToSlash(relPath)
					if info.IsDir() {
						header.Name += "/"
					} else {
						header.Method = zip.Deflate
					}
					writer, err := zw.CreateHeader(header)
					if err != nil {
						return err
					}
					if !info.IsDir() {
						f, err := os.Open(path)
						if err != nil {
							return err
						}
						defer f.Close()
						_, err = io.Copy(writer, f)
						return err
					}
					return nil
				})
				if walkErr != nil {
					break
				}
			} else {
				// Single file
				relPath, _ := filepath.Rel(basePath, selPath)
				header, err := zip.FileInfoHeader(stat)
				if err != nil {
					continue
				}
				header.Name = filepath.ToSlash(relPath)
				header.Method = zip.Deflate
				writer, err := zw.CreateHeader(header)
				if err != nil {
					continue
				}
				f, err := os.Open(selPath)
				if err != nil {
					continue
				}
				io.Copy(writer, f)
				f.Close()
			}
		}

		zw.Close()
		if walkErr != nil {
			pw.CloseWithError(walkErr)
		} else {
			pw.Close()
		}
	}()

	// Stream from pipe
	buf := make([]byte, chunkSize)
	var offset int64

	for {
		n, readErr := pr.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := sendFn(&pb.NodeMessage{
				RequestId: reqID,
				Payload: &pb.NodeMessage_Chunk{
					Chunk: &pb.DataChunk{Data: chunk, Offset: offset},
				},
			}); err != nil {
				pr.Close()
				return
			}
			offset += int64(n)
		}
		if readErr != nil {
			if readErr != io.EOF {
				log.Printf("gRPC: selective zip streaming error: %v", readErr)
				sendFn(errorMsg(reqID, 500, fmt.Sprintf("zip stream: %v", readErr)))
				return
			}
			break
		}
	}

	sendFn(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_TransferDone{
			TransferDone: &pb.TransferDone{TotalBytes: offset, Filename: filename},
		},
	})
}

func (h *StreamHandler) handleWrite(reqID, serverUUID string, req *pb.WriteFileReq) *pb.NodeMessage {
	if isProtectedFile(req.Path) {
		return errorMsg(reqID, 403, "cannot modify protected file")
	}
	filePath, err := h.validatePath(req.Path, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}

	// Ensure parent directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return errorMsg(reqID, 500, fmt.Sprintf("mkdir: %v", err))
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_Result{Result: &pb.OpResult{Message: "ready for chunks"}},
	}
}

// WriteChunksToFile writes incoming data chunks to a file. Called by the mesh manager.
func (h *StreamHandler) WriteChunksToFile(serverUUID, path string, data []byte) error {
	filePath, err := h.validatePath(path, serverUUID)
	if err != nil {
		return err
	}

	dir := filepath.Dir(filePath)
	os.MkdirAll(dir, 0755)

	return os.WriteFile(filePath, data, 0644)
}

// resolveFinalPath validates and returns the absolute path for a file write.
// Creates parent directories as needed.
func (h *StreamHandler) resolveFinalPath(serverUUID, path string) (string, error) {
	filePath, err := h.validatePath(path, serverUUID)
	if err != nil {
		return "", err
	}
	os.MkdirAll(filepath.Dir(filePath), 0755)
	return filePath, nil
}

// getTempDir returns the temp directory for upload staging.
func (h *StreamHandler) getTempDir() string {
	return filepath.Join(h.baseDir, "dylaris_data", ".tmp")
}

func (h *StreamHandler) handleCreate(reqID, serverUUID string, req *pb.CreateFileReq) *pb.NodeMessage {
	if isProtectedFile(req.Path) {
		return errorMsg(reqID, 403, "cannot modify protected file")
	}
	fullPath, err := h.validatePath(req.Path, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}

	if req.IsDir {
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return errorMsg(reqID, 500, fmt.Sprintf("mkdir: %v", err))
		}
	} else {
		dir := filepath.Dir(fullPath)
		os.MkdirAll(dir, 0755)
		f, err := os.Create(fullPath)
		if err != nil {
			return errorMsg(reqID, 500, fmt.Sprintf("create: %v", err))
		}
		f.Close()
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_Result{Result: &pb.OpResult{Message: "created"}},
	}
}

func (h *StreamHandler) handleDelete(reqID, serverUUID string, req *pb.DeleteFileReq) *pb.NodeMessage {
	if isProtectedFile(req.Path) {
		return errorMsg(reqID, 403, "cannot delete protected file")
	}
	fullPath, err := h.validatePath(req.Path, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}

	if err := os.RemoveAll(fullPath); err != nil {
		return errorMsg(reqID, 500, fmt.Sprintf("delete: %v", err))
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_Result{Result: &pb.OpResult{Message: "deleted"}},
	}
}

func (h *StreamHandler) handleRename(reqID, serverUUID string, req *pb.RenameFileReq) *pb.NodeMessage {
	if isProtectedFile(req.OldPath) {
		return errorMsg(reqID, 403, "cannot rename protected file")
	}
	oldPath, err := h.validatePath(req.OldPath, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}

	// New name is just the filename, keep same parent directory
	dir := filepath.Dir(oldPath)
	newName := sanitizeFilename(req.NewName)
	if newName == "" {
		return errorMsg(reqID, 400, "invalid filename")
	}
	if newName == ".active_server" {
		return errorMsg(reqID, 403, "cannot use protected filename")
	}
	newPath := filepath.Join(dir, newName)

	if err := os.Rename(oldPath, newPath); err != nil {
		return errorMsg(reqID, 500, fmt.Sprintf("rename: %v", err))
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_Result{Result: &pb.OpResult{Message: "renamed"}},
	}
}

func (h *StreamHandler) handleCopy(reqID, serverUUID string, req *pb.CopyFileReq) *pb.NodeMessage {
	if isProtectedFile(req.DstPath) {
		return errorMsg(reqID, 403, "cannot overwrite protected file")
	}
	srcPath, err := h.validatePath(req.SrcPath, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}
	dstPath, err := h.validatePath(req.DstPath, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}

	stat, err := os.Stat(srcPath)
	if err != nil {
		return errorMsg(reqID, 404, "source not found")
	}

	if stat.IsDir() {
		if err := copyDir(srcPath, dstPath); err != nil {
			return errorMsg(reqID, 500, fmt.Sprintf("copy dir: %v", err))
		}
	} else {
		if err := copyFile(srcPath, dstPath); err != nil {
			return errorMsg(reqID, 500, fmt.Sprintf("copy file: %v", err))
		}
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_Result{Result: &pb.OpResult{Message: "copied"}},
	}
}

// --- Helpers ---

// isProtectedFile checks if a path points to a protected system file.
func isProtectedFile(path string) bool {
	return filepath.Base(filepath.Clean(path)) == ".active_server"
}

func errorMsg(reqID string, code int32, message string) *pb.NodeMessage {
	return &pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_Error{
			Error: &pb.OpError{Code: code, Message: message},
		},
	}
}

func sanitizeFilename(name string) string {
	var result []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' || c == '+' {
			result = append(result, c)
		}
	}
	return string(result)
}

// copyFile and copyDir are defined in installer.go — reused here.
