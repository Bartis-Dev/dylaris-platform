package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/sftp"
)

// newProtectedFS builds a virtualFS over one real server directory containing a
// backup archive, and returns it with the on-disk path of that archive.
func newProtectedFS(t *testing.T) (*virtualFS, string) {
	t.Helper()
	sm, _ := newPlacementManager(t, 1)
	const uuid = "srv-uuid"
	base := filepath.Join(sm.Paths()[0], uuid)

	archive := filepath.Join(base, ".dylaris-backups", "20260806-105747.tar.gz")
	if err := os.MkdirAll(filepath.Dir(archive), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("archive-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".active_server"), []byte("survival"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "survival"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "survival", "server.properties"), []byte("motd=hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	return newVirtualFS([]sftpServerRef{{UUID: uuid, Name: "myserver"}}, sm, nil, "tester"), archive
}

// The guards took filepath.Base, which turns ".dylaris-backups/x.tar.gz" into
// an ordinary "x.tar.gz". Listing hid the directory, so a client could delete,
// truncate or move away backups it was never shown.
//
// Request paths are written without a leading slash: resolve prepends one
// itself, and on Windows filepath.Clean reads the resulting "//myserver" as a
// UNC share prefix. The node runs on Linux, where the two forms are the same
// input, so this keeps the test meaningful on both.
func TestSFTPGuardsSeeTheWholePath(t *testing.T) {
	const backup = "myserver/.dylaris-backups/20260806-105747.tar.gz"

	t.Run("remove", func(t *testing.T) {
		fs, archive := newProtectedFS(t)
		if err := fs.Filecmd(&sftp.Request{Method: "Remove", Filepath: backup}); err != os.ErrPermission {
			t.Errorf("Remove err = %v, want permission denied", err)
		}
		if _, err := os.Stat(archive); err != nil {
			t.Errorf("the archive was deleted: %v", err)
		}
	})

	t.Run("write truncates", func(t *testing.T) {
		fs, archive := newProtectedFS(t)
		if _, err := fs.Filewrite(&sftp.Request{Method: "Put", Filepath: backup}); err != os.ErrPermission {
			t.Errorf("Filewrite err = %v, want permission denied", err)
		}
		body, err := os.ReadFile(archive)
		if err != nil || string(body) != "archive-bytes" {
			t.Errorf("archive = %q (err %v), want it untouched", body, err)
		}
	})

	t.Run("rename away", func(t *testing.T) {
		fs, archive := newProtectedFS(t)
		err := fs.Filecmd(&sftp.Request{Method: "Rename", Filepath: backup, Target: "myserver/survival/stolen.tar.gz"})
		if err != os.ErrPermission {
			t.Errorf("Rename err = %v, want permission denied", err)
		}
		if _, err := os.Stat(archive); err != nil {
			t.Errorf("the archive was moved: %v", err)
		}
	})

	t.Run("rename onto a protected name", func(t *testing.T) {
		fs, _ := newProtectedFS(t)
		err := fs.Filecmd(&sftp.Request{
			Method:   "Rename",
			Filepath: "myserver/survival/server.properties",
			Target:   "myserver/.active_server",
		})
		if err != os.ErrPermission {
			t.Errorf("Rename err = %v, want permission denied", err)
		}
	})

	t.Run("read", func(t *testing.T) {
		fs, _ := newProtectedFS(t)
		if _, err := fs.Fileread(&sftp.Request{Method: "Get", Filepath: backup}); err != os.ErrPermission {
			t.Errorf("Fileread err = %v, want permission denied", err)
		}
	})

	t.Run("stat stays consistent with the listing filter", func(t *testing.T) {
		fs, _ := newProtectedFS(t)
		if _, err := fs.Filelist(&sftp.Request{Method: "Stat", Filepath: backup}); err != os.ErrNotExist {
			t.Errorf("Stat err = %v, want not exist", err)
		}
	})
}

// The guard must not swallow ordinary work, and clients stat the server
// directory itself on every navigation.
func TestSFTPOrdinaryPathsStillWork(t *testing.T) {
	fs, _ := newProtectedFS(t)

	rd, err := fs.Fileread(&sftp.Request{Method: "Get", Filepath: "myserver/survival/server.properties"})
	if err != nil {
		t.Errorf("reading an ordinary file: %v", err)
	}
	if c, ok := rd.(io.Closer); ok {
		c.Close() // Windows will not remove the temp dir with the handle open
	}
	if _, err := fs.Filelist(&sftp.Request{Method: "Stat", Filepath: "myserver"}); err != nil {
		t.Errorf("stat of the server directory: %v", err)
	}
	if _, err := fs.Filelist(&sftp.Request{Method: "List", Filepath: "myserver"}); err != nil {
		t.Errorf("listing the server directory: %v", err)
	}
	if err := fs.Filecmd(&sftp.Request{Method: "Mkdir", Filepath: "myserver/survival/plugins"}); err != nil {
		t.Errorf("mkdir of an ordinary directory: %v", err)
	}
}
