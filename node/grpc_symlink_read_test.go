package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	pb "dylaris-proto/node"
)

// The archive walkers drop a symlink that leaves the server directory, but the
// containment guard itself (resolveWithinDir) is purely lexical: it cleans and
// prefix-checks a string and never asks the filesystem where the path actually
// leads. Every non-archive file operation therefore reaches the target of a
// planted link - a plain single-file download is the shortest route.
//
// A tenant plants one from inside their own Minecraft container (the server
// directory is bind-mounted into it) or over SFTP. The link text is resolved on
// the NODE's side, so it can name a path that exists only there.
func TestFileReadDoesNotFollowALinkOutOfTheServerDirectory(t *testing.T) {
	h, uuid, secret := seedControlPlaneTree(t)

	var body bytes.Buffer
	var errMessage string
	h.HandleStreaming(&pb.NodeMessage{
		RequestId:  "r1",
		ServerUuid: uuid,
		Payload: &pb.NodeMessage_ReadReq{
			ReadReq: &pb.ReadFileReq{Path: "plugins/escape.txt"},
		},
	}, func(m *pb.NodeMessage) error {
		if c := m.GetChunk(); c != nil {
			body.Write(c.Data)
		}
		if e := m.GetError(); e != nil {
			errMessage = e.Message
		}
		return nil
	})

	if bytes.Contains(body.Bytes(), []byte("TOP SECRET")) {
		t.Errorf("downloading a planted symlink returned %s, a file outside the server directory", secret)
	}
	if errMessage == "" {
		t.Error("the read was not refused")
	}

	// A link that stays inside the server directory is an ordinary file and
	// must keep working, otherwise the guard is just "break every symlink".
	body.Reset()
	errMessage = ""
	h.HandleStreaming(&pb.NodeMessage{
		RequestId:  "r2",
		ServerUuid: uuid,
		Payload: &pb.NodeMessage_ReadReq{
			ReadReq: &pb.ReadFileReq{Path: "plugins/inside.txt"},
		},
	}, func(m *pb.NodeMessage) error {
		if c := m.GetChunk(); c != nil {
			body.Write(c.Data)
		}
		if e := m.GetError(); e != nil {
			errMessage = e.Message
		}
		return nil
	})
	if errMessage != "" {
		t.Errorf("a contained symlink was refused: %s", errMessage)
	}
	if body.String() != "motd=hi" {
		t.Errorf("contained symlink read %q, want %q", body.String(), "motd=hi")
	}
}

// Copy is the same read primitive with a second step: it dereferences the link
// and writes a REGULAR file holding the target's bytes into the tenant's own
// directory, which they can then download through the ordinary path.
func TestFileCopyDoesNotDereferenceALinkOutOfTheServerDirectory(t *testing.T) {
	h, uuid, _ := seedControlPlaneTree(t)

	msg := h.handleCopy("r1", uuid, &pb.CopyFileReq{
		SrcPath: "plugins/escape.txt",
		DstPath: "stolen.txt",
	})
	if msg.GetError() == nil {
		t.Error("copying a symlink that leaves the server directory was accepted")
	}
	stolen, err := os.ReadFile(filepath.Join(h.serverDir(uuid), "stolen.txt"))
	if err == nil && bytes.Contains(stolen, []byte("TOP SECRET")) {
		t.Error("the copy materialised outside content as a plain file inside the server directory")
	}

	// The same walk, one level up: the link is not named, it is merely inside
	// the directory being copied.
	msg = h.handleCopy("r2", uuid, &pb.CopyFileReq{
		SrcPath: "plugins",
		DstPath: "plugins-copy",
	})
	if msg.GetError() != nil {
		t.Fatalf("copying a directory that happens to contain a link failed outright: %s", msg.GetError().Message)
	}
	copied, err := os.ReadFile(filepath.Join(h.serverDir(uuid), "plugins-copy", "escape.txt"))
	if err == nil && bytes.Contains(copied, []byte("TOP SECRET")) {
		t.Error("a directory copy dereferenced a link that leaves the server directory")
	}
	// The rest of the directory still has to arrive.
	if b, err := os.ReadFile(filepath.Join(h.serverDir(uuid), "plugins-copy", "ok.jar")); err != nil || string(b) != "JAR" {
		t.Errorf("the ordinary file was lost from the copy: %q, %v", b, err)
	}
}
