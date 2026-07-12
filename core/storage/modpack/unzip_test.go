package modpack

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// buildZip creates an in-memory zip archive from the given entries (name ->
// content). archive/zip.Writer.Create does not validate entry names, so this
// lets the traversal/unsafe-path tests below construct real archives with
// intentionally malicious entry names.
func buildZip(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create entry %q: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("write entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}

func TestIsUnsafeEntryPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"safe nested path", "mods/example.jar", false},
		{"safe path with backslashes", `mods\subfolder\file.jar`, false},
		{"leading slash is absolute", "/etc/passwd", true},
		{"unc-style double slash", "//server/share/file", true},
		{"parent traversal single segment", "../evil.jar", true},
		{"parent traversal after backslash normalize", `..\evil.jar`, true},
		{"parent traversal embedded mid-path", "a/../../b", true},
		{"parent traversal deep inside safe-looking path", "mods/config/../../../etc/passwd", true},
		{"uppercase windows drive letter", `C:\evil.exe`, true},
		{"lowercase windows drive letter", `d:\evil.exe`, true},
		{"drive-letter-like prefix without slash", "D:file.txt", true},
		{"empty path", "", false},
		{"single char path", "a", false},
		{"dotdot as part of a longer segment, not a traversal", "mods/..config/file.txt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnsafeEntryPath(tc.path); got != tc.want {
				t.Errorf("IsUnsafeEntryPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestHasUnsafeZipEntry(t *testing.T) {
	t.Run("all safe entries", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{
			"mods/example.jar": []byte("jar-bytes"),
			"config/x.txt":     []byte("config"),
		})
		if HasUnsafeZipEntry(zipBytes) {
			t.Error("expected a fully safe archive to report no unsafe entry")
		}
	})

	t.Run("one traversal entry among safe ones", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{
			"mods/example.jar": []byte("jar-bytes"),
			"../../evil.jar":   []byte("evil"),
		})
		if !HasUnsafeZipEntry(zipBytes) {
			t.Error("expected the traversal entry to be detected")
		}
	})

	t.Run("absolute path entry", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{
			"/etc/passwd": []byte("evil"),
		})
		if !HasUnsafeZipEntry(zipBytes) {
			t.Error("expected the absolute-path entry to be detected")
		}
	})

	t.Run("empty archive is safe", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{})
		if HasUnsafeZipEntry(zipBytes) {
			t.Error("expected an empty archive to report no unsafe entry")
		}
	})

	t.Run("unreadable bytes are treated as unsafe", func(t *testing.T) {
		if !HasUnsafeZipEntry([]byte("not a zip file")) {
			t.Error("expected unreadable zip bytes to be treated as unsafe")
		}
	})
}

func TestReadZipEntry(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 100)
	zipBytes := buildZip(t, map[string][]byte{
		"sub/file.txt": content,
	})

	t.Run("reads a matching entry under the cap", func(t *testing.T) {
		data, ok := ReadZipEntry(zipBytes, "sub/file.txt", 200)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if !bytes.Equal(data, content) {
			t.Errorf("data mismatch: got %d bytes, want %d bytes", len(data), len(content))
		}
	})

	t.Run("rejects an entry over the declared-size cap without decompressing", func(t *testing.T) {
		_, ok := ReadZipEntry(zipBytes, "sub/file.txt", 50)
		if ok {
			t.Error("expected ok=false when the entry's declared size exceeds maxBytes")
		}
	})

	t.Run("accepts an entry exactly at the cap boundary", func(t *testing.T) {
		data, ok := ReadZipEntry(zipBytes, "sub/file.txt", int64(len(content)))
		if !ok || len(data) != len(content) {
			t.Errorf("expected ok=true with %d bytes, got ok=%v len=%d", len(content), ok, len(data))
		}
	})

	t.Run("matches a backslash-style innerPath against a forward-slash entry", func(t *testing.T) {
		data, ok := ReadZipEntry(zipBytes, `sub\file.txt`, 200)
		if !ok || !bytes.Equal(data, content) {
			t.Errorf("expected the backslash path to normalize and match, ok=%v", ok)
		}
	})

	t.Run("missing entry", func(t *testing.T) {
		_, ok := ReadZipEntry(zipBytes, "sub/does-not-exist.txt", 200)
		if ok {
			t.Error("expected ok=false for a missing entry")
		}
	})

	t.Run("unreadable zip bytes", func(t *testing.T) {
		_, ok := ReadZipEntry([]byte("not a zip"), "sub/file.txt", 200)
		if ok {
			t.Error("expected ok=false for unreadable zip bytes")
		}
	})
}

func TestFirstInnerJar(t *testing.T) {
	t.Run("finds the jar, skipping non-jar files", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{
			"config/readme.txt": []byte("not a jar"),
			"mods/example.jar":  []byte("jar-content"),
		})
		name, data, ok := FirstInnerJar(zipBytes)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if name != "example.jar" {
			t.Errorf("name = %q, want %q", name, "example.jar")
		}
		if string(data) != "jar-content" {
			t.Errorf("data = %q, want %q", data, "jar-content")
		}
	})

	t.Run("case-insensitive .jar suffix match", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{
			"mods/Example.JAR": []byte("jar-content"),
		})
		name, _, ok := FirstInnerJar(zipBytes)
		if !ok || name != "Example.JAR" {
			t.Errorf("expected the .JAR entry to match, got name=%q ok=%v", name, ok)
		}
	})

	t.Run("no jar entry present", func(t *testing.T) {
		zipBytes := buildZip(t, map[string][]byte{
			"config/readme.txt": []byte("not a jar"),
		})
		_, _, ok := FirstInnerJar(zipBytes)
		if ok {
			t.Error("expected ok=false when the archive has no .jar entry")
		}
	})

	t.Run("unreadable zip bytes", func(t *testing.T) {
		_, _, ok := FirstInnerJar([]byte("not a zip"))
		if ok {
			t.Error("expected ok=false for unreadable zip bytes")
		}
	})

	t.Run("decompression-bomb cap rejects an oversize jar entry", func(t *testing.T) {
		// Builds a REAL zip whose single entry decompresses to
		// maxInnerJarBytes+1 (512 MiB + 1 byte). All-zero content is highly
		// compressible, so the zip itself stays ~500KiB and this subtest
		// finishes in about a second - the only way to exercise the real cap
		// without touching production code: maxInnerJarBytes is an
		// unexported const, and unlike ReadZipEntry, FirstInnerJar does not
		// pre-check declared size before reading, so only actually exceeding
		// the cap while reading trips the ok=false branch.
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, err := zw.Create("mods/bomb.jar")
		if err != nil {
			t.Fatalf("create bomb entry: %v", err)
		}
		if _, err := io.CopyN(w, zeroReader{}, maxInnerJarBytes+1); err != nil {
			t.Fatalf("write bomb entry: %v", err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("close zip writer: %v", err)
		}
		_, _, ok := FirstInnerJar(buf.Bytes())
		if ok {
			t.Error("expected ok=false for a jar entry exceeding maxInnerJarBytes")
		}
	})
}

// zeroReader streams an unbounded run of zero bytes without materializing
// the whole payload in memory, so the bomb-cap test above can cheaply build
// a >512MiB decompressed entry. io.CopyN wraps it in an io.LimitReader, so
// zeroReader itself never needs to signal EOF.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
