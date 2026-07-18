package storage

import (
	"io"
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
	if err := p.WriteFile("sub/hello.txt", strings.NewReader("hi there")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rc, err := p.GetFile("sub/hello.txt")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "hi there" {
		t.Errorf("GetFile content = %q, want %q", got, "hi there")
	}
	url, err := p.DownloadURL("sub/hello.txt", time.Minute)
	if err != nil {
		t.Fatalf("DownloadURL err = %v, want nil", err)
	}
	if url != "" {
		t.Errorf("DownloadURL = %q, want \"\" (local streams via caller)", url)
	}
}
