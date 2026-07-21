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

// Size caps bound the memory a single server-pack render can consume, since it
// is reachable from the public share endpoint. A deflate zip-bomb inside an
// owner-uploaded stored zip could otherwise decompress fully into RAM. These
// mirror the 512 MiB per-entry cap the sibling unzip helpers already apply.
const (
	maxServerPackEntryBytes = 512 << 20 // 512 MiB per inner file
	maxServerPackTotalBytes = 2 << 30   // 2 GiB assembled pack
)

// hasOversizedZipEntry reports whether any entry in zipBytes declares an
// UncompressedSize64 over maxServerPackEntryBytes. Store-time defense in
// depth (BC2 bundled minor): even though the render paths (renderServerPack,
// writeMrpackZip) now cap decompression at read time, rejecting an oversized
// declared size at STORE time means a decompression bomb is never persisted
// in the first place. An unreadable zip is flagged as oversized too (fail
// closed), matching modpack.HasUnsafeZipEntry's convention.
func hasOversizedZipEntry(zipBytes []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return true
	}
	for _, f := range zr.File {
		if f.UncompressedSize64 > maxServerPackEntryBytes {
			return true
		}
	}
	return false
}

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
// renderServerPack assembles the server pack zip and writes it to dst as it
// goes, so peak memory is one content entry rather than the whole pack. It used
// to build the entire archive in a bytes.Buffer and return it - up to the 2 GiB
// cap held in RAM, per request, on the unauthenticated share route. Streaming
// bounds that to the largest single entry (itself capped) regardless of pack
// size or concurrency.
//
// The cost of streaming: once bytes have flowed to dst the HTTP status is
// committed, so an error partway through cannot become a clean 500. The caller
// tracks whether anything was written and reports a clean error only while none
// has. Every security check (unsafe zip entries, the size caps, unsafe target
// paths) still runs BEFORE its entry is written, so a rejected entry aborts the
// stream without its bytes ever reaching the client.
func (h *PacksHandler) renderServerPack(ctx context.Context, content []models.BuildContentEntry, dst io.Writer) error {
	prov, err := modpack.NewProviderFromSettings(h.state.Store.GetSetting, h.state.buildCoreStorageProvider)
	if err != nil {
		return err
	}
	if prov == nil {
		return fmt.Errorf("modpack storage not configured")
	}
	zw := zip.NewWriter(dst)
	var total int64 // bounds the assembled pack across both source branches
	for _, e := range content {
		if e.Side == models.SideClient {
			continue // client-only content is not part of a server pack
		}
		switch e.Source {
		case models.SourceUpload:
			if e.StorageKey == "" {
				return fmt.Errorf("upload content %q missing storage key", e.ModSlug)
			}
			raw, err := prov.Get(ctx, e.StorageKey)
			if err != nil {
				return fmt.Errorf("read stored content %q: %w", e.ModSlug, err)
			}
			// These bytes are re-served to whoever extracts the zip, so a
			// traversal-bearing entry name would zip-slip the operator's box.
			if modpack.HasUnsafeZipEntry(raw) {
				return fmt.Errorf("stored content %q has unsafe entry paths", e.ModSlug)
			}
			zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				return fmt.Errorf("unzip stored content %q: %w", e.ModSlug, err)
			}
			for _, f := range zr.File {
				if f.FileInfo().IsDir() {
					continue
				}
				if f.UncompressedSize64 > maxServerPackEntryBytes {
					return fmt.Errorf("stored content %q entry %q exceeds the size cap", e.ModSlug, f.Name)
				}
				rc, err := f.Open()
				if err != nil {
					return err
				}
				b, err := io.ReadAll(io.LimitReader(rc, maxServerPackEntryBytes+1))
				rc.Close()
				if err != nil {
					return err
				}
				if int64(len(b)) > maxServerPackEntryBytes {
					return fmt.Errorf("stored content %q entry %q exceeds the size cap", e.ModSlug, f.Name)
				}
				total += int64(len(b))
				if total > maxServerPackTotalBytes {
					return fmt.Errorf("server pack exceeds the total size cap")
				}
				if err := writeServerPackEntry(zw, f.Name, b); err != nil {
					return err
				}
			}
		case models.SourceModrinth:
			if e.ModrinthDownloadURL == "" || e.SHA1 == "" {
				return fmt.Errorf("modrinth content %q missing download URL or sha1", e.ModSlug)
			}
			if e.TargetPath == "" || modpack.IsUnsafeEntryPath(e.TargetPath) {
				return fmt.Errorf("modrinth content %q has an invalid target path", e.ModSlug)
			}
			jar, err := services.DownloadModrinthJar(ctx, e.ModrinthDownloadURL, e.SHA1, e.SHA512)
			if err != nil {
				return fmt.Errorf("download %q: %w", e.ModSlug, err)
			}
			total += int64(len(jar))
			if total > maxServerPackTotalBytes {
				return fmt.Errorf("server pack exceeds the total size cap")
			}
			if err := writeServerPackEntry(zw, e.TargetPath, jar); err != nil {
				return err
			}
		default:
			return fmt.Errorf("content %q has unsupported source %q", e.ModSlug, e.Source)
		}
	}
	return zw.Close()
}

// countingWriter passes writes through and records how many bytes have gone to
// the wrapped writer. A streaming handler uses written==0 to tell "failed
// before anything was sent" (a clean error status is still possible) from
// "failed mid-stream" (the status is already committed, only a log is left).
type countingWriter struct {
	w       io.Writer
	written int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.written += int64(n)
	return n, err
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
