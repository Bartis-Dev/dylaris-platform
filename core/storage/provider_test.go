package storage

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewProvider_PathAndLocalResolveToLocal(t *testing.T) {
	for _, typ := range []string{"", "local", "path"} {
		t.Run("type="+typ, func(t *testing.T) {
			p, err := NewProvider(typ, t.TempDir(), nil)
			if err != nil {
				t.Fatalf("NewProvider(%q) err = %v, want nil", typ, err)
			}
			if _, ok := p.(*LocalProvider); !ok {
				t.Fatalf("NewProvider(%q) = %T, want *LocalProvider", typ, p)
			}
		})
	}
}

func TestNewProvider_S3StubErrors(t *testing.T) {
	if _, err := NewProvider("s3", "", nil); err == nil {
		t.Fatal("NewProvider(\"s3\") err = nil, want a not-available error")
	}
}

func TestNewProvider_UnknownErrors(t *testing.T) {
	if _, err := NewProvider("ftp", "", nil); err == nil {
		t.Fatal("NewProvider(\"ftp\") err = nil, want unknown-provider error")
	}
}

func TestLocalProvider_RoundTripAndDownloadURL(t *testing.T) {
	p := &LocalProvider{BasePath: t.TempDir()}
	if err := p.WriteFile(context.Background(), "sub/hello.txt", strings.NewReader("hi there")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rc, err := p.GetFile(context.Background(), "sub/hello.txt")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hi there" {
		t.Errorf("GetFile content = %q, want %q", got, "hi there")
	}
	url, err := p.DownloadURL(context.Background(), "sub/hello.txt", time.Minute)
	if err != nil {
		t.Fatalf("DownloadURL err = %v, want nil", err)
	}
	if url != "" {
		t.Errorf("DownloadURL = %q, want \"\" (local streams via caller)", url)
	}
}

// failingReader delivers prefix and then fails, standing in for a transfer that
// dies halfway: a client that disconnects, or a mounted share that goes away.
type failingReader struct {
	prefix string
	off    int
	err    error
}

func (r *failingReader) Read(b []byte) (int, error) {
	if r.off >= len(r.prefix) {
		return 0, r.err
	}
	n := copy(b, r.prefix[r.off:])
	r.off += n
	return n, nil
}

// A failed write must not be observable at the destination key: before the
// staging rename it truncated the real file and left the partial bytes there.
func TestLocalProvider_WriteFileFailureLeavesDestinationIntact(t *testing.T) {
	tests := []struct {
		name    string
		pre     string // content written first, "" for no pre-existing file
		wantErr error
	}{
		{name: "overwrite keeps the old content", pre: "original content", wantErr: errors.New("connection reset")},
		{name: "fresh write creates nothing", pre: "", wantErr: errors.New("connection reset")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			p := &LocalProvider{BasePath: base}
			if tt.pre != "" {
				if err := p.WriteFile(context.Background(), "sub/f.bin", strings.NewReader(tt.pre)); err != nil {
					t.Fatalf("seeding WriteFile: %v", err)
				}
			}

			err := p.WriteFile(context.Background(), "sub/f.bin", &failingReader{prefix: "partial", err: tt.wantErr})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteFile err = %v, want %v", err, tt.wantErr)
			}

			got, readErr := os.ReadFile(filepath.Join(base, "sub", "f.bin"))
			if tt.pre == "" {
				if !os.IsNotExist(readErr) {
					t.Errorf("destination exists after a failed fresh write: content %q, err %v", got, readErr)
				}
			} else {
				if readErr != nil {
					t.Fatalf("reading destination: %v", readErr)
				}
				if string(got) != tt.pre {
					t.Errorf("destination content = %q, want the untouched %q", got, tt.pre)
				}
			}

			entries, err := os.ReadDir(filepath.Join(base, "sub"))
			if err != nil {
				t.Fatalf("ReadDir: %v", err)
			}
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), uploadTempPrefix) {
					t.Errorf("staging file %q was left behind after a failed write", e.Name())
				}
			}
		})
	}
}

func TestLocalProvider_WriteFileLeavesNoStagingFile(t *testing.T) {
	base := t.TempDir()
	p := &LocalProvider{BasePath: base}
	if err := p.WriteFile(context.Background(), "f.txt", strings.NewReader("done")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "f.txt" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory after WriteFile = %v, want only [f.txt]", names)
	}
}

// cancelMidStreamReader delivers one chunk, cancels the context, then reports
// EOF. The EOF matters: without a ctx-aware copy the write COMPLETES instead of
// hanging, so a missing guard shows up as a clean assertion failure below
// rather than as a test that blocks forever.
type cancelMidStreamReader struct {
	cancel context.CancelFunc
	chunk  string
	reads  int
}

func (r *cancelMidStreamReader) Read(b []byte) (int, error) {
	r.reads++
	if r.reads > 1 {
		return 0, io.EOF
	}
	n := copy(b, r.chunk)
	r.cancel()
	return n, nil
}

// TestLocalProvider_WriteFileHonoursContext pins that WriteFile actually
// CONSULTS its ctx rather than merely accepting one. A cancelled transfer must
// report the context error and must leave nothing behind: no file at the
// destination key, and no staging orphan.
//
// What this deliberately does NOT claim: that cancellation interrupts a
// filesystem call already blocked on a wedged mount. It cannot, and no ctx can.
// See ctxReader.
//
// On which guard each case actually pins, verified by mutation: the mid-stream
// case is the only one that pins the ctxReader in the io.Copy - removing it
// fails that case alone. The two pre-cancelled cases do NOT uniquely pin
// WriteFile's entry ctx.Err() check, because the ctxReader catches an
// already-cancelled ctx on the very first Read anyway; they fail only when both
// guards are gone. The entry check is a fast path that avoids creating the
// destination directory and staging file for a request that is already dead.
func TestLocalProvider_WriteFileHonoursContext(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T) (context.Context, io.Reader)
		wantErr error
	}{
		{
			name: "cancelled before the call",
			setup: func(t *testing.T) (context.Context, io.Reader) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, strings.NewReader("payload")
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline already expired",
			setup: func(t *testing.T) (context.Context, io.Reader) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
				t.Cleanup(cancel)
				return ctx, strings.NewReader("payload")
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "cancelled mid-stream",
			setup: func(t *testing.T) (context.Context, io.Reader) {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				return ctx, &cancelMidStreamReader{cancel: cancel, chunk: "partial"}
			},
			wantErr: context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			p := &LocalProvider{BasePath: base}
			ctx, body := tt.setup(t)

			err := p.WriteFile(ctx, "sub/f.bin", body)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("WriteFile err = %v, want %v", err, tt.wantErr)
			}

			if _, statErr := os.Stat(filepath.Join(base, "sub", "f.bin")); !os.IsNotExist(statErr) {
				t.Errorf("destination exists after a cancelled write (stat err = %v), want it absent", statErr)
			}

			// Walk the whole base: the staging file lives next to the
			// destination, and on the entry-guard paths that directory is
			// never created at all.
			walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && strings.HasPrefix(d.Name(), uploadTempPrefix) {
					t.Errorf("staging file %q left behind after a cancelled write", path)
				}
				return nil
			})
			if walkErr != nil {
				t.Fatalf("WalkDir: %v", walkErr)
			}
		})
	}
}

// An orphan from a killed transfer must not be offered as a real file: it is a
// partial the caller could download, copy into a server, or delete.
func TestLocalProvider_ListFilesHidesStagingFiles(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, uploadTempPrefix+"1234"), []byte("half"), 0644); err != nil {
		t.Fatalf("seeding orphan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "real.txt"), []byte("ok"), 0644); err != nil {
		t.Fatalf("seeding real file: %v", err)
	}
	p := &LocalProvider{BasePath: base}
	files, err := p.ListFiles(context.Background(), "")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].Name != "real.txt" {
		t.Errorf("ListFiles = %+v, want only real.txt", files)
	}
}
