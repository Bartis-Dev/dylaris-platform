package main

import (
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	pb "dylaris-proto/node"
)

// hashFilesMaxCount bounds one request. Identifying unknown jars is a
// user-triggered action over a directory listing, not a sweep, so a few dozen
// is the realistic size and anything far past it is a malformed request.
const hashFilesMaxCount = 200

// hashFilesMaxSize bounds a single file. A mod jar is single-digit to low
// double-digit megabytes; something far larger in a mods directory is not a
// mod, and hashing it would only burn IO for a lookup that cannot match.
const hashFilesMaxSize = 512 << 20

// handleHashFiles hashes named files inside one directory of a server.
//
// It exists because a jar placed by hand (SFTP, beam, a file-manager upload)
// has no database row saying which Modrinth project it is, and Modrinth can
// only be asked "which version is this" by file hash. Hashing on the node means
// the bytes never cross the wire just to be summed.
//
// Names are plain file names and are checked to be so. Anything carrying a
// separator is refused rather than resolved: the request names files inside the
// directory it already asked for, and a path there would be an attempt to leave
// it.
func (h *StreamHandler) handleHashFiles(reqID, serverUUID string, req *pb.HashFilesReq) *pb.NodeMessage {
	dirPath, err := h.validatePath(req.Path, serverUUID)
	if err != nil {
		return errorMsg(reqID, 403, err.Error())
	}
	if len(req.Names) > hashFilesMaxCount {
		return errorMsg(reqID, 400, fmt.Sprintf("too many files in one request (max %d)", hashFilesMaxCount))
	}

	out := make([]*pb.FileHash, 0, len(req.Names))
	for _, name := range req.Names {
		fh := &pb.FileHash{Name: name}
		if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
			fh.Error = "not a plain file name"
			out = append(out, fh)
			continue
		}
		full := filepath.Join(dirPath, name)
		stat, err := os.Stat(full)
		if err != nil {
			fh.Error = "not found"
			out = append(out, fh)
			continue
		}
		if stat.IsDir() {
			fh.Error = "is a directory"
			out = append(out, fh)
			continue
		}
		if stat.Size() > hashFilesMaxSize {
			fh.Error = "file is too large to hash"
			out = append(out, fh)
			continue
		}
		sha1hex, sha512hex, err := hashFileBoth(full)
		if err != nil {
			fh.Error = err.Error()
			out = append(out, fh)
			continue
		}
		fh.Sha1 = sha1hex
		fh.Sha512 = sha512hex
		fh.Size = stat.Size()
		out = append(out, fh)
	}

	return &pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_HashFilesResp{HashFilesResp: &pb.HashFilesResp{Files: out}},
	}
}

// hashFileBoth streams a file once and produces both digests. Modrinth's lookup
// endpoint takes sha1 or sha512 and the two callers want different ones, so
// computing both in one pass is cheaper than deciding later. (installer_modpack.go
// has hashFile, which is sha512-only and used to verify a declared download.)
func hashFileBoth(path string) (sha1hex, sha512hex string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	h1 := sha1.New()
	h512 := sha512.New()
	if _, err := io.Copy(io.MultiWriter(h1, h512), f); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h1.Sum(nil)), hex.EncodeToString(h512.Sum(nil)), nil
}
