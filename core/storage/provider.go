package storage

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileInfo represents a file or directory in the storage
type FileInfo struct {
	Name    string `json:"name"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Enabled bool   `json:"enabled"` // false when admin has disabled this path
}

// StorageProvider is the interface for all core-storage backends (library,
// ticket attachments, ticket backups). Keys are forward-slash-separated and
// provider-relative.
//
// How far ctx actually reaches differs per backend, so do not read it as a
// blanket cancellation guarantee. S3Provider hands it to every SDK call, so a
// cancelled ctx aborts the in-flight HTTP request. LocalProvider only checks
// it on entry and between copy chunks: a filesystem syscall that is already
// blocked (a hung NFS or CIFS mount) cannot be interrupted from userspace, and
// no ctx will change that.
type StorageProvider interface {
	ListFiles(ctx context.Context, path string) ([]FileInfo, error)
	GetFile(ctx context.Context, path string) (io.ReadCloser, error)
	DeletePath(ctx context.Context, path string) error
	CreateDir(ctx context.Context, path string) error
	// CopyToLocal copies a file/dir from storage to a local destination path.
	// If the source is a .zip, it gets extracted into destPath.
	CopyToLocal(ctx context.Context, srcPath, destPath string) error
	// WriteFile stores an uploaded file into storage at the given path.
	WriteFile(ctx context.Context, path string, content io.Reader) error
	// DownloadURL returns a short-lived pre-signed GET URL when the backend
	// supports it (S3). LocalProvider has no such URL and returns ("", nil)
	// so the caller streams the bytes through Core instead of redirecting.
	// Callers must treat an error as "cannot redirect" and fall through to
	// streaming, never conflate it with "no URL available".
	DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Opt keys accepted by NewProvider for the "s3" backend.
const (
	OptS3Endpoint  = "endpoint"
	OptS3Bucket    = "bucket"
	OptS3Region    = "region"
	OptS3AccessKey = "accessKey"
	OptS3SecretKey = "secretKey"
	OptS3PathStyle = "pathStyle"
	OptS3Prefix    = "prefix"
)

// NewProvider builds a core StorageProvider. "" / "local" / "path" resolve to
// LocalProvider (canonical name "path"; "local" kept for back-compat with the
// legacy library_type value). "s3" builds an S3Provider (see s3provider.go).
func NewProvider(storageType, basePath string, opts map[string]string) (StorageProvider, error) {
	switch storageType {
	case "", "local", "path":
		return &LocalProvider{BasePath: basePath}, nil
	case "s3":
		return newS3ProviderFromOpts(opts)
	default:
		return nil, fmt.Errorf("storage: unknown provider %q", storageType)
	}
}

// ==========================================
// LOCAL PROVIDER
// ==========================================

type LocalProvider struct {
	BasePath string
}

func (p *LocalProvider) validatePath(reqPath string) (string, error) {
	fullPath := filepath.Join(p.BasePath, reqPath)
	cleanPath := filepath.Clean(fullPath)
	if !strings.HasPrefix(cleanPath, filepath.Clean(p.BasePath)) {
		return "", fmt.Errorf("access denied: path outside library root")
	}
	return cleanPath, nil
}

func (p *LocalProvider) ListFiles(ctx context.Context, path string) ([]FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	safePath, err := p.validatePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []FileInfo{}, nil
		}
		return nil, err
	}

	var files []FileInfo
	for _, e := range entries {
		// Orphaned upload staging files (see WriteFile). Listing them would
		// offer a partial file for download and let a user delete or copy it.
		if strings.HasPrefix(e.Name(), uploadTempPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			Enabled: true, // default; library handler overrides based on disabled set
		})
	}
	if files == nil {
		files = []FileInfo{}
	}
	return files, nil
}

func (p *LocalProvider) GetFile(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	safePath, err := p.validatePath(path)
	if err != nil {
		return nil, err
	}
	return os.Open(safePath)
}

func (p *LocalProvider) DeletePath(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	safePath, err := p.validatePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(safePath)
}

func (p *LocalProvider) CreateDir(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	safePath, err := p.validatePath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(safePath, 0755)
}

// uploadTempPrefix marks the half-written files WriteFile leaves behind when a
// transfer dies between the copy and the rename. It is deliberately a fixed,
// recognisable prefix so those orphans can be swept and so ListFiles can hide
// them: a caller must never be handed a partial upload as if it were a file.
const uploadTempPrefix = ".dylaris-upload-"

// ctxReader aborts a stream copy between chunks: once ctx is done, Read
// reports the context error instead of handing over the next chunk.
//
// This is deliberately the whole mechanism, and it is worth being clear about
// its limit. It stops a copy that is still making progress (a client that
// disconnected mid-upload, a job that was cancelled), which is what an
// io.Copy over a network body needs. It cannot unblock a Read or Write that is
// ALREADY stuck inside a syscall on a wedged mount, because nothing in
// userspace can. That case needs the watchdog, not a context.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func newCtxReader(ctx context.Context, r io.Reader) io.Reader {
	return &ctxReader{ctx: ctx, r: r}
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// WriteFile stages the upload next to its destination and renames it into
// place, so the destination name only ever refers to a complete file.
//
// Writing straight into the destination meant a transfer that died halfway -
// a dropped client, or a mounted share going away mid-copy - left a TRUNCATED
// file under the real name, which the next read served as if it were valid.
// Silent corruption; this is the fix.
//
// The temp file is created in the destination directory because rename cannot
// cross filesystems, and the destination directory is the only place
// guaranteed to be on the same one.
//
// Caveat worth knowing: rename is atomic on local filesystems, but neither
// CIFS nor NFS guarantees it. On those this is a large improvement over
// truncate-in-place, not a guarantee.
func (p *LocalProvider) WriteFile(ctx context.Context, path string, content io.Reader) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	safePath, err := p.validatePath(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(safePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, uploadTempPrefix+"*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()

	// CreateTemp is 0600. The previous implementation used os.Create (0666
	// before umask), and files here are read by other processes when the base
	// path is a share, so keep the old mode rather than silently tightening it.
	if err := tmp.Chmod(0644); err != nil {
		return err
	}
	if _, err := io.Copy(tmp, newCtxReader(ctx, content)); err != nil {
		return err
	}
	// Sync before rename, not for crash durability but for error reporting: on
	// a network filesystem a buffered write can be accepted here and fail on
	// flush. Without this the rename would publish a file whose tail never
	// landed.
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, safePath); err != nil {
		return err
	}
	committed = true
	return nil
}

func (p *LocalProvider) CopyToLocal(ctx context.Context, srcPath, destPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	safeSrc, err := p.validatePath(srcPath)
	if err != nil {
		return err
	}

	if strings.HasSuffix(strings.ToLower(safeSrc), ".zip") {
		return extractZip(ctx, safeSrc, destPath)
	}

	info, err := os.Stat(safeSrc)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(ctx, safeSrc, destPath)
	}
	return copyFile(ctx, safeSrc, filepath.Join(destPath, info.Name()))
}

func (p *LocalProvider) DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", nil
}

// ==========================================
// HELPERS
// ==========================================

func extractZip(ctx context.Context, src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		fpath := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(filepath.Clean(fpath), filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}
		out, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, newCtxReader(ctx, rc))
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func copyDir(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyPath(ctx, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyPath(ctx context.Context, src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(ctx, src, dst)
	}
	return copyFile(ctx, src, dst)
}

func copyFile(ctx context.Context, src, dst string) error {
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
	_, err = io.Copy(out, newCtxReader(ctx, in))
	return err
}
