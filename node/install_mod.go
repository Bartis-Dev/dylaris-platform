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
	cleanName := filepath.Base(pl.FileName)
	if cleanName == "" || cleanName == "." || cleanName == ".." || strings.ContainsAny(cleanName, "/\\") {
		log.Printf("install_mod: invalid file name %q", pl.FileName)
		return
	}
	// GetServerDir returns the path up to <uuid>; we tack on the active
	// sub-server + targetDir.
	destDir := filepath.Join(serverPath, pl.ActiveSubServer, pl.TargetDir)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		log.Printf("install_mod: mkdir %s: %v", destDir, err)
		return
	}
	destFile := filepath.Join(destDir, cleanName)
	tmpFile := destFile + ".part"

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
	cleanName := filepath.Base(pl.FileName)
	if cleanName == "" || cleanName == "." || cleanName == ".." || strings.ContainsAny(cleanName, "/\\") {
		log.Printf("remove_mod: invalid file name %q", pl.FileName)
		return
	}
	path := filepath.Join(serverPath, pl.ActiveSubServer, pl.TargetDir, cleanName)
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
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer f.Close()

	h := sha512.New()
	// Cap downloads at 256 MB so a runaway / malicious URL can't fill the disk.
	const maxSize = 256 << 20
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxSize+1)); err != nil {
		return fmt.Errorf("copy: %w", err)
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
