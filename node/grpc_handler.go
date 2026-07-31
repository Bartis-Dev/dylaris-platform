package main

import (
	"archive/zip"
	"encoding/json"
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
	case *pb.NodeMessage_InspectOrphanReq:
		return []*pb.NodeMessage{h.handleInspectOrphan(msg.RequestId, msg.ServerUuid)}
	case *pb.NodeMessage_BackupListReq:
		return []*pb.NodeMessage{h.handleBackupList(msg.RequestId, msg.ServerUuid)}
	case *pb.NodeMessage_BackupDeleteReq:
		return []*pb.NodeMessage{h.handleBackupDelete(msg.RequestId, msg.ServerUuid, p.BackupDeleteReq)}
	case *pb.NodeMessage_BackupUsageReq:
		return []*pb.NodeMessage{h.handleBackupUsage(msg.RequestId, msg.ServerUuid)}
	case *pb.NodeMessage_RconExecReq:
		return []*pb.NodeMessage{h.handleRconExec(msg.RequestId, msg.ServerUuid, p.RconExecReq)}
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

	// BackupOpenReq: stream one .dylaris-backups/<key>.tar.gz to Core. Path
	// resolution is the same as a regular file read but pinned to the hidden
	// backup directory so Core can't accidentally fetch arbitrary files
	// using the backup-open RPC.
	if openReq := msg.GetBackupOpenReq(); openReq != nil {
		h.streamBackupArchive(msg.RequestId, msg.ServerUuid, openReq, sendFn)
		return
	}

	readReq := msg.GetReadReq()
	if readReq == nil {
		sendFn(errorMsg(msg.RequestId, 400, "HandleStreaming only supports ReadReq/SelectiveReadReq/BackupOpenReq"))
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

// resolveWithinDir joins reqPath under dataPath and guarantees the result
// stays inside dataPath. It is the single source of truth for the
// path-traversal guard shared by the gRPC file handler (validatePath), the
// selective-zip download path, and the Beam server (validateBeamPathOp).
//
// Containment is checked with a trailing separator so a sibling directory that
// merely shares the prefix (e.g. dataPath+"-evil") cannot pass — without it,
// reqPath "../<base>-evil/secret" would resolve to a sibling and slip through.
func resolveWithinDir(dataPath, reqPath string) (string, error) {
	cleanPath := filepath.Clean(filepath.Join(dataPath, reqPath))
	cleanData := filepath.Clean(dataPath)
	if cleanPath != cleanData && !strings.HasPrefix(cleanPath, cleanData+string(os.PathSeparator)) {
		return "", fmt.Errorf("access denied: path traversal")
	}
	return cleanPath, nil
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

	return resolveWithinDir(dataPath, reqPath)
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
		if isProtectedFile(e.Name()) {
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
			// Security: ensure each selected entry stays within basePath
			// (trailing-separator containment, shared with validatePath).
			selPath, err := resolveWithinDir(basePath, sel)
			if err != nil {
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

// createUploadTemp opens the staging file for one inbound mesh upload.
//
// It is created in the FINAL directory, not a shared temp dir: the transfer
// completes with an os.Rename, and as soon as STORAGE_PATHS points at its own
// mount, a staging dir under the working directory is a different filesystem,
// so that rename fails with EXDEV and every upload 500s. Same approach the beam
// upload path already takes. The leading dot keeps the partial file out of the
// way in the file browser.
func (h *StreamHandler) createUploadTemp(serverUUID, path string) (*os.File, error) {
	finalPath, err := h.resolveFinalPath(serverUUID, path)
	if err != nil {
		return nil, err
	}
	return os.CreateTemp(filepath.Dir(finalPath), ".upload-*.tmp")
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

// handleInspectOrphan returns metadata, active sub-server, and sub-server scan
// for a server UUID. Pure read — performs no mutation.
func (h *StreamHandler) handleInspectOrphan(reqID, serverUUID string) *pb.NodeMessage {
	if serverUUID == "" {
		return errorMsg(reqID, 400, "server_uuid required")
	}

	var serverDir string
	if h.storageMgr != nil {
		serverDir = h.storageMgr.GetServerDir(serverUUID)
	} else {
		serverDir = filepath.Join(h.baseDir, "dylaris_data", "servers", serverUUID)
	}

	resp := &pb.InspectOrphanResp{}

	// Read .dylaris.json — missing or malformed is not an error, just has_metadata=false.
	if m, err := readServerMetadata(serverDir); err == nil {
		data, jsonErr := json.Marshal(m)
		if jsonErr == nil {
			resp.HasMetadata = true
			resp.MetadataJson = string(data)
		} else {
			log.Printf("handleInspectOrphan: marshal metadata for %s: %v", serverUUID, jsonErr)
		}
	}

	// Read .active_server — absent is fine, leave ActiveSubServer as "".
	activeBytes, err := os.ReadFile(filepath.Join(serverDir, ".active_server"))
	if err == nil {
		resp.ActiveSubServer = strings.TrimSpace(string(activeBytes))
	}

	// Scan sub-server directories.
	// SubServerInfo intentionally carries only name + type; the full
	// per-sub-server fields (minecraft_version, build, extra_jvm_flags) are
	// available to Core via the metadata_json field of the response.
	for _, sub := range scanSubServers(serverDir) {
		resp.SubServers = append(resp.SubServers, &pb.SubServerInfo{
			Name: sub.Name,
			Type: sub.Type,
		})
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_InspectOrphanResp{InspectOrphanResp: resp},
	}
}

// --- Helpers ---

// isProtectedFile checks if a path points to a protected system file.
//
// Used by the gRPC file handlers and the SFTP virtual-FS adapter to reject
// writes/deletes/renames against names the platform manages internally.
// SFTP additionally uses this set as a *listing* filter (entries excluded
// here never show up in Readdir / Stat output), which is why
// ".dylaris-backups" is included — node-local backups must stay completely
// invisible from the SFTP file browser, including the parent directory.
//
// The check also fires when ANY component of the path is .dylaris-backups,
// so a user that somehow guesses ".dylaris-backups/foo.tar.gz" can't write
// or rename it through the regular file API. The dedicated backup RPCs
// (handleBackupList/Open/Delete in grpc_backup.go) reach those files
// without going through this guard.
func isProtectedFile(path string) bool {
	clean := filepath.Clean(path)
	name := filepath.Base(clean)
	if name == ".active_server" || name == ".node_config.json" || name == ".dylaris-backups" || name == ".dylaris.json" {
		return true
	}
	// .pending-delete-* are short-lived rename targets used by the
	// sub-server delete pipeline -- the dir is moved here so the
	// browser stops seeing the original name immediately, with
	// async RemoveAll cleaning up the tombstone in the background.
	// Either way the user has no business seeing it.
	if strings.HasPrefix(name, ".pending-delete-") {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(clean), "/") {
		if part == ".dylaris-backups" || strings.HasPrefix(part, ".pending-delete-") {
			return true
		}
	}
	return false
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
