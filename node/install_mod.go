package main

// Install/remove a single mod or plugin file inside a server's
// sub-server tree. Source URL is always cdn.modrinth.com (enforced core-side
// in handlers/server_mods.go before the queue dispatch, but we re-check here
// since a stale queued payload is technically a stale trust boundary).
//
// install_mod payload (from core):
//   uuid, activeSubServer, targetDir ("mods"|"plugins"), fileName, downloadUrl, sha512
//
// remove_mod payload (from core):
//   uuid, activeSubServer, targetDir, fileName
//
// Every field below that becomes part of a path is re-checked here for that
// reason, activeSubServer included - see validActiveSubServer.

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dylaris-pkg/validate"
)

type installModPayload struct {
	UUID            string `json:"uuid"`
	ActiveSubServer string `json:"activeSubServer"`
	TargetDir       string `json:"targetDir"`
	FileName        string `json:"fileName"`
	DownloadURL     string `json:"downloadUrl"`
	SHA512          string `json:"sha512"`
}

type removeModPayload struct {
	UUID            string `json:"uuid"`
	ActiveSubServer string `json:"activeSubServer"`
	TargetDir       string `json:"targetDir"`
	FileName        string `json:"fileName"`
}

func runInstallMod(storage *StorageManager, payload string) {
	var pl installModPayload
	if err := json.Unmarshal([]byte(payload), &struct {
		Config *installModPayload `json:"config"`
	}{Config: &pl}); err != nil {
		log.Printf("install_mod: decode failed: %v", err)
		return
	}
	if err := validateModrinthURL(pl.DownloadURL); err != nil {
		log.Printf("install_mod: %v", err)
		return
	}
	serverPath := storage.GetServerDir(pl.UUID)
	if serverPath == "" {
		log.Printf("install_mod: storage lookup for %s returned empty path", pl.UUID)
		return
	}
	if !validSubDir(pl.TargetDir) {
		log.Printf("install_mod: invalid target dir %q", pl.TargetDir)
		return
	}
	if !validActiveSubServer(pl.ActiveSubServer) {
		log.Printf("install_mod: invalid active sub-server %q", pl.ActiveSubServer)
		return
	}
	cleanName := filepath.Base(pl.FileName)
	if cleanName == "" || cleanName == "." || cleanName == ".." || strings.ContainsAny(cleanName, "/\\") {
		log.Printf("install_mod: invalid file name %q", pl.FileName)
		return
	}
	// GetServerDir returns the path up to <uuid>; the active sub-server and
	// targetDir are joined on through resolveWithinDir - the same traversal AND
	// symlink boundary the file browser, the beam server and the zip walkers go
	// through.
	//
	// This file joined the path by hand instead. Every check it did make
	// (validSubDir, validActiveSubServer, filepath.Base) guards the STRING; none
	// of them asks the filesystem, and the directory being joined onto is
	// bind-mounted into the tenant's OWN Minecraft container as /data. "ln -s
	// /app/dylaris_data mods" is one line in there. After it, os.MkdirAll adopts
	// the link and os.Create follows it, so installing a mod wrote a
	// Modrinth-hosted file - content the tenant picks, by publishing it - to any
	// path the node process can write, and removing one deleted any file it can
	// delete. resolveWithinDir refuses a component that resolves outside the
	// server root, and refuses a DANGLING link too: that one is a trap for
	// whatever creates the file next.
	//
	// Checked before MkdirAll on purpose - MkdirAll through a dangling link
	// creates the target.
	destDir, err := resolveWithinDir(serverPath, filepath.Join(pl.ActiveSubServer, pl.TargetDir))
	if err != nil {
		log.Printf("install_mod: %v", err)
		return
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Printf("install_mod: mkdir %s: %v", destDir, err)
		return
	}
	// The .part file is the one the download actually opens, so it is the one
	// that has to be clean; destFile is only ever reached by os.Rename, which
	// does not follow a link.
	tmpFile, err := resolveWithinDir(destDir, cleanName+".part")
	if err != nil {
		log.Printf("install_mod: %v", err)
		return
	}
	destFile := filepath.Join(destDir, cleanName)

	if err := downloadAndVerify(pl.DownloadURL, tmpFile, pl.SHA512); err != nil {
		os.Remove(tmpFile)
		log.Printf("install_mod: download failed for %s: %v", cleanName, err)
		return
	}
	if err := os.Rename(tmpFile, destFile); err != nil {
		os.Remove(tmpFile)
		log.Printf("install_mod: rename %s → %s: %v", tmpFile, destFile, err)
		return
	}
	log.Printf("install_mod: installed %s into %s", cleanName, destDir)
}

func runRemoveMod(storage *StorageManager, payload string) {
	var pl removeModPayload
	if err := json.Unmarshal([]byte(payload), &struct {
		Config *removeModPayload `json:"config"`
	}{Config: &pl}); err != nil {
		log.Printf("remove_mod: decode failed: %v", err)
		return
	}
	serverPath := storage.GetServerDir(pl.UUID)
	if serverPath == "" {
		log.Printf("remove_mod: storage lookup for %s returned empty path", pl.UUID)
		return
	}
	if !validSubDir(pl.TargetDir) {
		log.Printf("remove_mod: invalid target dir %q", pl.TargetDir)
		return
	}
	if !validActiveSubServer(pl.ActiveSubServer) {
		log.Printf("remove_mod: invalid active sub-server %q", pl.ActiveSubServer)
		return
	}
	cleanName := filepath.Base(pl.FileName)
	if cleanName == "" || cleanName == "." || cleanName == ".." || strings.ContainsAny(cleanName, "/\\") {
		log.Printf("remove_mod: invalid file name %q", pl.FileName)
		return
	}
	// Same boundary as the install side: os.Remove does not follow the leaf, but
	// a symlinked "mods" directory would still put the deletion outside the
	// server root.
	dir, err := resolveWithinDir(serverPath, filepath.Join(pl.ActiveSubServer, pl.TargetDir))
	if err != nil {
		log.Printf("remove_mod: %v", err)
		return
	}
	path := filepath.Join(dir, cleanName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("remove_mod: rm %s: %v", path, err)
		return
	}
	log.Printf("remove_mod: removed %s", path)
}

func validateModrinthURL(u string) error {
	if !strings.HasPrefix(u, "https://cdn.modrinth.com/") {
		return fmt.Errorf("downloadUrl must point to cdn.modrinth.com (got %q)", truncate(u, 60))
	}
	return nil
}

func validSubDir(d string) bool {
	return d == "mods" || d == "plugins"
}

// validActiveSubServer is the check the header comment above already promised
// and this file did not make. Both functions filepath.Join the field onto the
// server's data directory, and Join CLEANS rather than confines: a name carrying
// ".." walks out of the server root, which for install_mod means writing a
// downloaded jar outside it and for remove_mod means deleting a file outside it.
// targetDir and fileName were both re-checked here; this one was not, and it is
// the field that arrives from a .active_server file on a node's own disk.
//
// Same rule Core applies (validate.IsSubServerName): one plain directory name.
// Empty is allowed - a server with no sub-server keeps its files at the root and
// Join skips an empty element.
func validActiveSubServer(s string) bool {
	return s == "" || validate.IsSubServerName(s)
}

func downloadAndVerify(url, dest, expectedSHA512 string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	// Remove-then-O_EXCL rather than os.Create: the caller resolved this path a
	// moment ago, but the tenant can re-plant a link at it from inside their
	// container between that check and this open. os.Remove takes the link
	// itself, O_EXCL refuses anything that reappears. Same pair backup_worker.go
	// uses for node-local archives.
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear previous partial download: %w", err)
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()

	h := sha512.New()
	// Cap downloads at 256 MB so a runaway / malicious URL can't fill the disk.
	//
	// The extra byte is the overflow probe, and it has to be ACTED on: without
	// the length check below, an oversized response was cut off at the cap and
	// reported as a successful download, so with no sha512 in the payload a
	// truncated jar got renamed into place and the server crash-looped on a
	// corrupt archive with nothing pointing at the size. Same shape as the bug in
	// downloadFileBounded (installer_modpack.go).
	const maxSize = 256 << 20
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	if n > maxSize {
		return fmt.Errorf("download exceeds the %d byte limit", int64(maxSize))
	}
	if expectedSHA512 != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, expectedSHA512) {
			return fmt.Errorf("sha512 mismatch: want %s got %s", expectedSHA512, got)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
