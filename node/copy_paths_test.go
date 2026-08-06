package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One copy request with an empty source and destination destroyed a whole
// server: both ends resolve to the server root, filepath.Walk visits every
// file, and copyFile opens each one with O_TRUNC as its own source. 185 files
// went to zero bytes - the world, every config, and the three dotfiles
// isProtectedFile exists to defend. That guard never fired, because it only
// inspects the destination string and filepath.Base("") is ".".
func TestValidateCopyPaths(t *testing.T) {
	root := filepath.FromSlash("/data/servers/abc")
	tests := []struct {
		name           string
		rawSrc, rawDst string
		src, dst       string
		wantErr        string
	}{
		{
			name:   "the request that wiped the server",
			rawSrc: "", rawDst: "",
			src: root, dst: root,
			wantErr: "both a source and a destination",
		},
		{
			name:   "empty source alone",
			rawSrc: "  ", rawDst: "survival/copy",
			src: root, dst: filepath.Join(root, "survival/copy"),
			wantErr: "both a source and a destination",
		},
		{
			name:   "empty destination alone",
			rawSrc: "survival/server.properties", rawDst: "",
			src: filepath.Join(root, "survival/server.properties"), dst: root,
			wantErr: "both a source and a destination",
		},
		{
			name:   "a path onto itself",
			rawSrc: "survival", rawDst: "survival",
			src: filepath.Join(root, "survival"), dst: filepath.Join(root, "survival"),
			wantErr: "onto itself",
		},
		{
			name:   "a directory into itself",
			rawSrc: "survival", rawDst: "survival/backup",
			src: filepath.Join(root, "survival"), dst: filepath.Join(root, "survival/backup"),
			wantErr: "into itself",
		},
		{
			name:   "an ordinary file copy",
			rawSrc: "survival/server.properties", rawDst: "survival/server.properties.bak",
			src: filepath.Join(root, "survival/server.properties"),
			dst: filepath.Join(root, "survival/server.properties.bak"),
		},
		{
			name:   "a sibling directory copy",
			rawSrc: "survival", rawDst: "creative",
			src: filepath.Join(root, "survival"), dst: filepath.Join(root, "creative"),
		},
		{
			name:   "a subdirectory out to its parent's sibling",
			rawSrc: "survival/world", rawDst: "worlds-archive",
			src: filepath.Join(root, "survival/world"), dst: filepath.Join(root, "worlds-archive"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCopyPaths(tt.rawSrc, tt.rawDst, tt.src, tt.dst)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("want the copy allowed, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// isProtectedFile only sees the path a request names. A directory copy reaches
// the protected files on its own, so copyDir has to skip them itself.
func TestCopyDirSkipsProtectedFiles(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	write := func(rel, body string) {
		full := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".active_server", "survival")
	write(".dylaris.json", "{}")
	write(".node_config.json", "{}")
	write(filepath.Join(".dylaris-backups", "old.tar.gz"), "archive")
	write(filepath.Join("survival", "server.properties"), "motd=hi")

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	for _, rel := range []string{".active_server", ".dylaris.json", ".node_config.json",
		filepath.Join(".dylaris-backups", "old.tar.gz")} {
		if _, err := os.Stat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("%s was copied, want it skipped (stat err = %v)", rel, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(dst, "survival", "server.properties"))
	if err != nil {
		t.Fatalf("the ordinary file should still be copied: %v", err)
	}
	if string(body) != "motd=hi" {
		t.Errorf("copied content = %q, want %q", body, "motd=hi")
	}
}
