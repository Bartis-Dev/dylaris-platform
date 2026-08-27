package services

import (
	"errors"
	"testing"

	"dylaris-core/models"
	"dylaris-core/services/redisacl"
	"dylaris-core/store"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// sftpPruneFakeStore answers only the three reads sync() makes. The embedded
// nil store.Store makes any other call panic loudly rather than return a zero
// value that quietly changes what is under test.
type sftpPruneFakeStore struct {
	store.Store

	users     []models.User
	nodes     []models.Node
	access    map[int][]store.SFTPAccess
	accessErr map[int]error
}

func (f *sftpPruneFakeStore) ListUsers() ([]models.User, error) { return f.users, nil }
func (f *sftpPruneFakeStore) ListNodes() ([]models.Node, error) { return f.nodes, nil }
func (f *sftpPruneFakeStore) GetSFTPAccessByNode(nodeID int) ([]store.SFTPAccess, error) {
	if err := f.accessErr[nodeID]; err != nil {
		return nil, err
	}
	return f.access[nodeID], nil
}

// newSFTPPruneTest wires the service to a miniredis and returns both.
func newSFTPPruneTest(t *testing.T, fs *sftpPruneFakeStore) (*SFTPSyncService, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewSFTPSyncService(fs, rdb, nil), mr
}

const (
	pruneNodeA = "aaaaaaaa-0000-4000-8000-000000000001"
	pruneNodeB = "bbbbbbbb-0000-4000-8000-000000000002"
)

func pruneFixture() *sftpPruneFakeStore {
	return &sftpPruneFakeStore{
		users: []models.User{
			{Username: "alice", Password: "$2a$10$hash-alice"},
			{Username: "bob", Password: "$2a$10$hash-bob"},
		},
		nodes: []models.Node{
			{ID: 1, Token: pruneNodeA, Name: "node-a"},
			{ID: 2, Token: pruneNodeB, Name: "node-b"},
		},
		access: map[int][]store.SFTPAccess{
			1: {{ServerID: 10, UserID: "u-alice", Username: "alice", IsOwner: true, ServerUUID: "srv-a", ServerName: "A"}},
			2: {{ServerID: 20, UserID: "u-bob", Username: "bob", IsOwner: true, ServerUUID: "srv-b", ServerName: "B"}},
		},
		accessErr: map[int]error{},
	}
}

// A database error on ONE node's access query must not log every user on that
// node out of SFTP.
//
// The prune deletes every sftp:auth:* key it did not just publish. When
// GetSFTPAccessByNode fails, that node contributes nothing to the published
// set - so the prune could not tell "this user lost access" from "Core never
// got to ask", and answered the second question by deleting the credential.
// The hashes carry a 5-minute TTL precisely so a stalled sync degrades slowly;
// pruning on an unanswered query turned a transient blip into an immediate
// lockout that lasts until the next successful tick.
func TestATransientAccessErrorDoesNotDeleteThatNodesPublishedHashes(t *testing.T) {
	fs := pruneFixture()
	svc, mr := newSFTPPruneTest(t, fs)

	svc.sync() // healthy tick: both nodes publish

	keyA := redisacl.SFTPAuthKey(pruneNodeA, "alice")
	keyB := redisacl.SFTPAuthKey(pruneNodeB, "bob")
	for _, k := range []string{keyA, keyB} {
		if !mr.Exists(k) {
			t.Fatalf("setup: %s was never published", k)
		}
	}

	fs.accessErr[1] = errors.New("connection reset by peer")
	svc.sync()

	if !mr.Exists(keyA) {
		t.Errorf("a database error on node-a's access query deleted %s: every SFTP user on that node is locked out until the next successful tick", keyA)
	}
	if !mr.Exists(keyB) {
		t.Errorf("%s was deleted, and node-b answered normally", keyB)
	}
}

// The prune must still do its job: a user who genuinely lost access on a node
// that DID answer loses the credential on the next tick, rather than riding
// the 5-minute TTL out.
func TestRevokedAccessOnAHealthyNodeIsStillPruned(t *testing.T) {
	fs := pruneFixture()
	svc, mr := newSFTPPruneTest(t, fs)

	svc.sync()
	keyA := redisacl.SFTPAuthKey(pruneNodeA, "alice")
	if !mr.Exists(keyA) {
		t.Fatalf("setup: %s was never published", keyA)
	}

	fs.access[1] = nil // alice's access to node-a is revoked
	svc.sync()

	if mr.Exists(keyA) {
		t.Errorf("%s survived a revocation on a node that answered fine", keyA)
	}
}

// Keys from before this was node-scoped sit at a bare "sftp:auth:<username>",
// which no node prefix covers. They were readable by EVERY node, so clearing
// them is the point of the prune and must not be skipped along with an
// unanswered node's keys.
func TestLegacyFleetWideAuthKeysAreAlwaysPruned(t *testing.T) {
	fs := pruneFixture()
	fs.accessErr[1] = errors.New("connection reset by peer")
	svc, mr := newSFTPPruneTest(t, fs)

	if err := mr.Set("sftp:auth:alice", "$2a$10$legacy"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc.sync()

	if mr.Exists("sftp:auth:alice") {
		t.Error("the pre-node-scoping fleet-wide key survived; every node can read it")
	}
}
