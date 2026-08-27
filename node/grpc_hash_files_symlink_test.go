package main

import (
	"os"
	"testing"

	pb "dylaris-proto/node"
)

// Hashing must not follow a link out of the server directory.
//
// The directory in the request goes through validatePath, which pins the
// symlink boundary. The NAMES did not: they were joined onto it with
// filepath.Join and then os.Stat'ed and opened, both of which follow links.
// Every sibling handler that takes a validated directory plus caller-supplied
// entries re-resolves each one (grpc_handler.go does exactly that for a zip
// selection); this one was the exception.
//
// A tenant plants the link from inside their own Minecraft container - the
// server directory is bind-mounted into it - or over SFTP, and then presses
// "identify unknown jars". What comes back for a path that exists only on the
// node is its SIZE, its sha1 and its sha512: a filesystem oracle over the whole
// host, including other tenants' directories, plus offline confirmation of any
// file whose content can be guessed.
func TestHashFilesDoesNotFollowALinkOutOfTheServerDirectory(t *testing.T) {
	h, uuid, secret := seedControlPlaneTree(t)
	secretInfo, err := os.Stat(secret)
	if err != nil {
		t.Fatal(err)
	}

	resp := h.handleHashFiles("r1", uuid, &pb.HashFilesReq{
		Path:  "plugins",
		Names: []string{"escape.txt", "inside.txt", "ok.jar"},
	})
	if e := resp.GetError(); e != nil {
		t.Fatalf("the whole request was refused: %s", e.Message)
	}
	files := resp.GetHashFilesResp().GetFiles()
	if len(files) != 3 {
		t.Fatalf("got %d results, want 3", len(files))
	}

	byName := map[string]*pb.FileHash{}
	for _, f := range files {
		byName[f.Name] = f
	}

	escaped := byName["escape.txt"]
	if escaped == nil {
		t.Fatal("no result for the planted link")
	}
	if escaped.Sha1 != "" || escaped.Sha512 != "" {
		t.Errorf("hashed a file outside the server directory: sha1=%q sha512=%q", escaped.Sha1, escaped.Sha512)
	}
	if escaped.Size == secretInfo.Size() && secretInfo.Size() != 0 {
		t.Errorf("reported the size of a file outside the server directory (%d bytes)", escaped.Size)
	}
	if escaped.Error == "" {
		t.Error("the planted link was answered without an error, so nothing tells the caller it was refused")
	}

	// A link that stays INSIDE the server directory is ordinary content and
	// must keep working - the rule is about leaving, not about links.
	inside := byName["inside.txt"]
	if inside == nil || inside.Error != "" || inside.Sha1 == "" {
		t.Errorf("a link staying inside the server directory was refused: %+v", inside)
	}

	plain := byName["ok.jar"]
	if plain == nil || plain.Error != "" || plain.Sha1 == "" || plain.Sha512 == "" {
		t.Errorf("an ordinary file was not hashed: %+v", plain)
	}
}

// The same boundary, expressed as the oracle it removes: a name that resolves
// outside must be indistinguishable from one that is simply not there.
func TestHashFilesDoesNotConfirmWhetherAnOutsidePathExists(t *testing.T) {
	h, uuid, _ := seedControlPlaneTree(t)

	resp := h.handleHashFiles("r1", uuid, &pb.HashFilesReq{
		Path:  "plugins",
		Names: []string{"escape.txt", "does-not-exist.jar"},
	})
	files := resp.GetHashFilesResp().GetFiles()
	if len(files) != 2 {
		t.Fatalf("got %d results, want 2", len(files))
	}
	for _, f := range files {
		if f.Sha1 != "" || f.Size != 0 {
			t.Errorf("%s leaked something about a path it must not reach: %+v", f.Name, f)
		}
	}
}
