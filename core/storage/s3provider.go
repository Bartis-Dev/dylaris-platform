package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"dylaris-core/storage/backup"
)

// objectStore is the S3-shaped seam S3Provider needs. *backup.S3Storage
// satisfies it; tests supply an in-memory fake.
type objectStore interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]backup.Object, error)
	DownloadURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// S3Provider implements StorageProvider against an S3-compatible object store.
// Every key is namespaced under `prefix` so one bucket can host the library,
// ticket-attachments and ticket-backups subsystems side by side.
type S3Provider struct {
	os     objectStore
	prefix string
}

// newS3ProviderFromOpts builds an S3Provider by marshalling the opt map into a
// backup.S3Config and reusing the existing backup.S3Storage client.
func newS3ProviderFromOpts(opts map[string]string) (StorageProvider, error) {
	if opts == nil {
		opts = map[string]string{}
	}
	cfg := backup.S3Config{
		Endpoint:        opts[OptS3Endpoint],
		Region:          opts[OptS3Region],
		Bucket:          opts[OptS3Bucket],
		AccessKeyID:     opts[OptS3AccessKey],
		SecretAccessKey: opts[OptS3SecretKey],
		ForcePathStyle:  opts[OptS3PathStyle] == "true",
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	client, err := backup.NewS3(context.Background(), raw)
	if err != nil {
		return nil, err
	}
	return &S3Provider{os: client, prefix: strings.Trim(opts[OptS3Prefix], "/")}, nil
}

// key maps a provider-relative path to a full object key (prefix + clean path).
// Uses "path" (always forward-slash, no OS/UNC semantics), not "path/filepath":
// S3 keys are never local filesystem paths, and filepath.Clean on Windows
// parses a leading "//" as a UNC prefix instead of collapsing it.
func (p *S3Provider) key(reqPath string) string {
	clean := strings.TrimPrefix(path.Clean("/"+reqPath), "/")
	if p.prefix == "" {
		return clean
	}
	if clean == "" {
		return p.prefix
	}
	return p.prefix + "/" + clean
}

// listPrefix returns the object prefix used to enumerate one directory level.
func (p *S3Provider) listPrefix(reqPath string) string {
	k := p.key(reqPath)
	if k == "" {
		return ""
	}
	return k + "/"
}

func (p *S3Provider) WriteFile(path string, content io.Reader) error {
	return p.os.Put(context.Background(), p.key(path), content, 0)
}

func (p *S3Provider) GetFile(path string) (io.ReadCloser, error) {
	return p.os.Get(context.Background(), p.key(path))
}

func (p *S3Provider) DeletePath(path string) error {
	// Delete the object at the key AND every object under it (dir semantics).
	ctx := context.Background()
	objs, err := p.os.List(ctx, p.key(path))
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return p.os.Delete(ctx, p.key(path))
	}
	for _, o := range objs {
		if err := p.os.Delete(ctx, o.Key); err != nil {
			return err
		}
	}
	return nil
}

// CreateDir is a no-op: object stores have no directories.
func (p *S3Provider) CreateDir(path string) error { return nil }

func (p *S3Provider) DownloadURL(key string, ttl time.Duration) (string, error) {
	return p.os.DownloadURL(context.Background(), p.key(key), ttl)
}

// ListFiles synthesizes one directory level from the flat key space: files are
// keys with no further "/" after the list prefix; dirs are the distinct first
// path segments of deeper keys.
func (p *S3Provider) ListFiles(path string) ([]FileInfo, error) {
	pfx := p.listPrefix(path)
	objs, err := p.os.List(context.Background(), pfx)
	if err != nil {
		return nil, err
	}
	seenDir := map[string]bool{}
	out := []FileInfo{}
	for _, o := range objs {
		rest := strings.TrimPrefix(o.Key, pfx)
		if rest == "" {
			continue
		}
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			name := rest[:i]
			if !seenDir[name] {
				seenDir[name] = true
				out = append(out, FileInfo{Name: name, IsDir: true, Enabled: true})
			}
			continue
		}
		out = append(out, FileInfo{Name: rest, IsDir: false, Size: o.Size, Enabled: true})
	}
	return out, nil
}

// CopyToLocal stages the object(s) into destPath. A .zip source is downloaded
// to a temp file and extracted; anything else is copied verbatim.
func (p *S3Provider) CopyToLocal(srcPath, destPath string) error {
	ctx := context.Background()
	if strings.HasSuffix(strings.ToLower(srcPath), ".zip") {
		rc, err := p.os.Get(ctx, p.key(srcPath))
		if err != nil {
			return err
		}
		defer rc.Close()
		tmp, err := os.CreateTemp("", "dylaris-s3-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, rc); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		return extractZip(tmp.Name(), destPath)
	}
	// Non-zip: copy every object under the source prefix into destPath,
	// preserving the relative layout.
	objs, err := p.os.List(ctx, p.key(srcPath))
	if err != nil {
		return err
	}
	if len(objs) == 0 {
		return fmt.Errorf("s3 CopyToLocal: no object at %q", srcPath)
	}
	base := p.key(srcPath)
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return err
	}
	for _, o := range objs {
		rel := strings.TrimPrefix(strings.TrimPrefix(o.Key, base), "/")
		if rel == "" {
			rel = filepath.Base(o.Key)
		}
		dst := filepath.Join(destPath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		rc, err := p.os.Get(ctx, o.Key)
		if err != nil {
			return err
		}
		f, err := os.Create(dst)
		if err != nil {
			rc.Close()
			return err
		}
		_, cerr := io.Copy(f, rc)
		f.Close()
		rc.Close()
		if cerr != nil {
			return cerr
		}
	}
	return nil
}
