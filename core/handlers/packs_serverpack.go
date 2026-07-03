package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/storage/modpack"
)

// renderServerPack builds a plain .zip of a build's SERVER-SIDE content + configs
// (every entry whose side is not client-only), each file placed at its
// .minecraft-relative path so a server operator can extract it into the server
// root. Modrinth-linked content is downloaded (hash-verified) and embedded;
// uploaded content is read from storage and its inner files copied in. Unlike the
// .mrpack render, a plain zip has no reference mechanism, so every file is
// embedded. That is heavier and only produced on a share-link download.
//
// It deliberately does NOT include a loader/server jar: the spec scopes a
// server-pack to "server-side content + configs". The operator installs the
// loader (Fabric/Forge/...) via the normal server-install flow.
func (h *PacksHandler) renderServerPack(ctx context.Context, content []models.BuildContentEntry) ([]byte, error) {
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting)
	if err != nil {
		return nil, err
	}
	if prov == nil {
		return nil, fmt.Errorf("modpack storage not configured")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range content {
		if e.Side == models.SideClient {
			continue // client-only content is not part of a server pack
		}
		switch e.Source {
		case models.SourceUpload:
			if e.StorageKey == "" {
				return nil, fmt.Errorf("upload content %q missing storage key", e.ModSlug)
			}
			raw, err := prov.Get(e.StorageKey)
			if err != nil {
				return nil, fmt.Errorf("read stored content %q: %w", e.ModSlug, err)
			}
			// These bytes are re-served to whoever extracts the zip, so a
			// traversal-bearing entry name would zip-slip the operator's box.
			if modpack.HasUnsafeZipEntry(raw) {
				return nil, fmt.Errorf("stored content %q has unsafe entry paths", e.ModSlug)
			}
			zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				return nil, fmt.Errorf("unzip stored content %q: %w", e.ModSlug, err)
			}
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				b, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					return nil, err
				}
				if err := writeServerPackEntry(zw, f.Name, b); err != nil {
					return nil, err
				}
			}
		case models.SourceModrinth:
			if e.ModrinthDownloadURL == "" || e.SHA1 == "" {
				return nil, fmt.Errorf("modrinth content %q missing download URL or sha1", e.ModSlug)
			}
			if e.TargetPath == "" || modpack.IsUnsafeEntryPath(e.TargetPath) {
				return nil, fmt.Errorf("modrinth content %q has an invalid target path", e.ModSlug)
			}
			jar, err := services.DownloadModrinthJar(ctx, e.ModrinthDownloadURL, e.SHA1, e.SHA512)
			if err != nil {
				return nil, fmt.Errorf("download %q: %w", e.ModSlug, err)
			}
			if err := writeServerPackEntry(zw, e.TargetPath, jar); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("content %q has unsupported source %q", e.ModSlug, e.Source)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeServerPackEntry writes one file into zw at name (deflate).
func writeServerPackEntry(zw *zip.Writer, name string, data []byte) error {
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}
