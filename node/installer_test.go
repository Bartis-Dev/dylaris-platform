package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractZipToDir_Normal(t *testing.T) {
	// Build a zip with one valid file
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	zipDir := t.TempDir()
	zipPath := filepath.Join(zipDir, "test.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	if err := extractZipToDir(zipPath, destDir); err != nil {
		t.Fatalf("extractZipToDir: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(destDir, "hello.txt"))
	if err != nil {
		t.Fatalf("expected hello.txt to exist: %v", err)
	}
	if string(content) != "hello world" {
		t.Errorf("content: got %q, want %q", string(content), "hello world")
	}
}

func TestExtractZipToDir_PathTraversal(t *testing.T) {
	// Build a zip with a path traversal entry "../evil.txt"
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fw, err := zw.Create("../evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("evil content")); err != nil {
		t.Fatal(err)
	}
	zw.Close()

	zipDir := t.TempDir()
	zipPath := filepath.Join(zipDir, "traversal.zip")
	if err := os.WriteFile(zipPath, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	destDir := t.TempDir()
	// Must not error — traversal entries are silently skipped
	if err := extractZipToDir(zipPath, destDir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// evil.txt must NOT exist one directory above destDir
	evilPath := filepath.Join(filepath.Dir(destDir), "evil.txt")
	if _, err := os.Stat(evilPath); !os.IsNotExist(err) {
		t.Fatal("path traversal guard failed: evil.txt was created outside destDir")
	}
}
