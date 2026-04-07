package storage

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileInfo represents a file or directory in the storage
type FileInfo struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// StorageProvider is the interface for all library storage backends
type StorageProvider interface {
	ListFiles(path string) ([]FileInfo, error)
	GetFile(path string) (io.ReadCloser, error)
	DeletePath(path string) error
	CreateDir(path string) error
	// CopyToLocal copies a file/dir from storage to a local destination path.
	// If the source is a .zip, it gets extracted into destPath.
	CopyToLocal(srcPath, destPath string) error
	// WriteFile stores an uploaded file into storage at the given path
	WriteFile(path string, content io.Reader) error
}

// NewProvider creates the appropriate StorageProvider based on type.
// Supported types: "local" (default)
func NewProvider(storageType, basePath string, opts map[string]string) StorageProvider {
	switch strings.ToLower(storageType) {
	case "local", "":
		return &LocalProvider{BasePath: basePath}
	default:
		return &LocalProvider{BasePath: basePath}
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

func (p *LocalProvider) ListFiles(path string) ([]FileInfo, error) {
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
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, FileInfo{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  info.Size(),
		})
	}
	if files == nil {
		files = []FileInfo{}
	}
	return files, nil
}

func (p *LocalProvider) GetFile(path string) (io.ReadCloser, error) {
	safePath, err := p.validatePath(path)
	if err != nil {
		return nil, err
	}
	return os.Open(safePath)
}

func (p *LocalProvider) DeletePath(path string) error {
	safePath, err := p.validatePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(safePath)
}

func (p *LocalProvider) CreateDir(path string) error {
	safePath, err := p.validatePath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(safePath, 0755)
}

func (p *LocalProvider) WriteFile(path string, content io.Reader) error {
	safePath, err := p.validatePath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(safePath), 0755); err != nil {
		return err
	}
	f, err := os.Create(safePath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, content)
	return err
}

func (p *LocalProvider) CopyToLocal(srcPath, destPath string) error {
	safeSrc, err := p.validatePath(srcPath)
	if err != nil {
		return err
	}

	if strings.HasSuffix(strings.ToLower(safeSrc), ".zip") {
		return extractZip(safeSrc, destPath)
	}

	info, err := os.Stat(safeSrc)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(safeSrc, destPath)
	}
	return copyFile(safeSrc, filepath.Join(destPath, info.Name()))
}

// ==========================================
// HELPERS
// ==========================================

func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
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
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
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
	_, err = io.Copy(out, in)
	return err
}
