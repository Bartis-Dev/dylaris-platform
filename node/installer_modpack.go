package main

// Install a Modrinth modpack (.mrpack format) as a fresh
// sub-server.
//
// .mrpack format reference (https://docs.modrinth.com/docs/modpacks/format):
//   - zip file
//   - modrinth.index.json at root: { formatVersion, game:"minecraft",
//       versionId, name, summary, files[], dependencies{minecraft, forge,
//       fabric-loader, quilt-loader, neoforge, ...} }
//   - overrides/ : tree of files copied verbatim into the server dir
//   - server-overrides/ : applied on top of overrides for server installs
//
// Each entry in files[] has { path, hashes:{sha1,sha512}, env:{client,server},
// downloads:[url, ...], fileSize }. We accept only downloads from a small
// allowlist (cdn.modrinth.com + the Modrinth-approved CDN hosts they
// publish) and skip files whose env.server == "unsupported".

import (
	"archive/zip"
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

type mrpackIndex struct {
	FormatVersion int                    `json:"formatVersion"`
	Game          string                 `json:"game"`
	VersionID     string                 `json:"versionId"`
	Name          string                 `json:"name"`
	Summary       string                 `json:"summary"`
	Files         []mrpackFile           `json:"files"`
	Dependencies  map[string]string      `json:"dependencies"`
}

type mrpackFile struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Env       map[string]string `json:"env,omitempty"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`
}

// modpackAllowedHosts mirrors Modrinth's published list of approved download
// hosts. Any URL outside this set is a sign of a tampered or self-hosted
// pack — V1 we just refuse those rather than try to whitelist user content.
var modpackAllowedHosts = map[string]bool{
	"cdn.modrinth.com":                true,
	"github.com":                      true,
	"raw.githubusercontent.com":       true,
	"gitlab.com":                      true,
	"edge.forgecdn.net":               true,
	"mediafilez.forgecdn.net":         true,
	"maven.fabricmc.net":              true,
	"maven.minecraftforge.net":        true,
}

const (
	maxMrpackSize       = 200 << 20  // 200 MB total mrpack archive
	maxModpackTotalSize = 4 << 30    // 4 GB cap on summed file sizes
	mrpackTempDir       = ".dylaris-mrpack"
)

func installModpack(destDir string, cfg InstallerConfig) error {
	if cfg.URL == "" {
		return fmt.Errorf("modpack installer requires URL")
	}
	if err := validateMrpackURL(cfg.URL); err != nil {
		return err
	}

	log.Printf("modpack: downloading %s for %s", cfg.URL, destDir)
	tmp := filepath.Join(destDir, mrpackTempDir)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", tmp, err)
	}
	defer os.RemoveAll(tmp)

	mrpackPath := filepath.Join(tmp, "pack.mrpack")
	if err := downloadFileBounded(cfg.URL, mrpackPath, maxMrpackSize); err != nil {
		return fmt.Errorf("download .mrpack: %w", err)
	}

	idx, err := readMrpackIndex(mrpackPath)
	if err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if idx.Game != "" && idx.Game != "minecraft" {
		return fmt.Errorf("unsupported game %q in mrpack", idx.Game)
	}

	if err := extractOverrides(mrpackPath, destDir); err != nil {
		return fmt.Errorf("extract overrides: %w", err)
	}

	// Fan out every listed file. Skip client-only.
	var totalBytes int64
	for _, f := range idx.Files {
		if env := f.Env["server"]; env == "unsupported" {
			continue
		}
		totalBytes += f.FileSize
		if totalBytes > maxModpackTotalSize {
			return fmt.Errorf("modpack exceeds %d byte cap", int64(maxModpackTotalSize))
		}
		if err := fetchModpackFile(destDir, f); err != nil {
			log.Printf("modpack: file %s failed: %v", f.Path, err)
			return fmt.Errorf("file %s: %w", f.Path, err)
		}
	}

	// Chain into the loader install. Dependencies map gives us the MC
	// version + loader version. Loader choice derives from which key is set.
	mcVersion := idx.Dependencies["minecraft"]
	if mcVersion == "" {
		return fmt.Errorf("modpack manifest missing minecraft dependency")
	}
	if fl := idx.Dependencies["fabric-loader"]; fl != "" {
		log.Printf("modpack: chaining fabric mc=%s loader=%s", mcVersion, fl)
		return installFabric(destDir, mcVersion, fl)
	}
	if ql := idx.Dependencies["quilt-loader"]; ql != "" {
		log.Printf("modpack: chaining quilt mc=%s loader=%s (using fabric installer as fallback)", mcVersion, ql)
		return installFabric(destDir, mcVersion, ql)
	}
	if fb := idx.Dependencies["forge"]; fb != "" {
		log.Printf("modpack: chaining forge mc=%s loader=%s", mcVersion, fb)
		return installForge(destDir, mcVersion, fb, cfg.JavaImage, cfg.ServerUUID)
	}
	if nf := idx.Dependencies["neoforge"]; nf != "" {
		log.Printf("modpack: chaining neoforge loader=%s", nf)
		return installNeoForge(destDir, nf, cfg.JavaImage, cfg.ServerUUID)
	}
	// No loader listed → vanilla.
	return installVanilla(destDir, mcVersion)
}

func validateMrpackURL(u string) error {
	if !strings.HasPrefix(u, "https://") {
		return fmt.Errorf(".mrpack url must be https")
	}
	rest := strings.TrimPrefix(u, "https://")
	slash := strings.IndexByte(rest, '/')
	if slash < 1 {
		return fmt.Errorf("bad .mrpack url")
	}
	host := rest[:slash]
	if !modpackAllowedHosts[host] {
		return fmt.Errorf(".mrpack url host %q not allowed", host)
	}
	return nil
}

func readMrpackIndex(path string) (*mrpackIndex, error) {
	rd, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	for _, f := range rd.File {
		if f.Name != "modrinth.index.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		var idx mrpackIndex
		if err := json.NewDecoder(rc).Decode(&idx); err != nil {
			return nil, err
		}
		return &idx, nil
	}
	return nil, fmt.Errorf("modrinth.index.json not found in archive")
}

// extractOverrides walks overrides/ and server-overrides/ inside the
// .mrpack zip and copies entries into destDir. server-overrides applies
// on top so server-only tweaks override client values for the same path.
func extractOverrides(mrpackPath, destDir string) error {
	rd, err := zip.OpenReader(mrpackPath)
	if err != nil {
		return err
	}
	defer rd.Close()

	prefixes := []string{"overrides/", "server-overrides/"}
	for _, prefix := range prefixes {
		for _, f := range rd.File {
			if !strings.HasPrefix(f.Name, prefix) || f.Name == prefix {
				continue
			}
			rel := strings.TrimPrefix(f.Name, prefix)
			if rel == "" {
				continue
			}
			if strings.Contains(rel, "..") {
				return fmt.Errorf("unsafe path in mrpack: %s", f.Name)
			}
			dst := filepath.Join(destDir, rel)
			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(dst, 0o755); err != nil {
					return err
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			out, err := os.Create(dst)
			if err != nil {
				rc.Close()
				return err
			}
			if _, err := io.Copy(out, rc); err != nil {
				rc.Close()
				out.Close()
				return err
			}
			rc.Close()
			out.Close()
		}
	}
	return nil
}

func fetchModpackFile(destDir string, f mrpackFile) error {
	if len(f.Downloads) == 0 {
		return fmt.Errorf("no download URLs")
	}
	if strings.Contains(f.Path, "..") {
		return fmt.Errorf("unsafe path %q", f.Path)
	}
	dst := filepath.Join(destDir, filepath.FromSlash(f.Path))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	var lastErr error
	for _, u := range f.Downloads {
		if err := validateMrpackURL(u); err != nil {
			lastErr = err
			continue
		}
		tmp := dst + ".part"
		if err := downloadFileBounded(u, tmp, f.FileSize+(64<<10)); err != nil {
			lastErr = err
			continue
		}
		// Verify sha512 if provided.
		if want := f.Hashes["sha512"]; want != "" {
			got, err := hashFile(tmp)
			if err != nil {
				os.Remove(tmp)
				lastErr = err
				continue
			}
			if !strings.EqualFold(got, want) {
				os.Remove(tmp)
				lastErr = fmt.Errorf("sha512 mismatch for %s", f.Path)
				continue
			}
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			return err
		}
		return nil
	}
	return lastErr
}

// downloadFileBounded streams a URL to disk with a sane timeout + size cap.
// Named distinctly from the installer.go downloadFile (which is unbounded
// and used by single-jar installers) so we don't shadow that helper.
func downloadFileBounded(url, dst string, maxBytes int64) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if maxBytes <= 0 {
		maxBytes = maxModpackTotalSize
	}
	if _, err := io.Copy(out, io.LimitReader(resp.Body, maxBytes+1)); err != nil {
		return err
	}
	return nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
