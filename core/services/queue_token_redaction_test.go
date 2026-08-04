package services

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"

	"dylaris-core/services/redisacl"
)

// aclFailingStore makes EnsureForToken fail on its first call, which is what
// drives SendCommand's warning log. Only NodeIDByToken is reached; the rest of
// HandshakeStore is embedded so the fake stays a fake.
type aclFailingStore struct {
	redisacl.HandshakeStore
}

func (aclFailingStore) NodeIDByToken(string) (int, bool, error) {
	return 0, false, errors.New("redis: connection refused")
}

// TestSendCommand_DoesNotLogTheWholeNodeToken pins a log-redaction rule the
// rest of the codebase already follows.
//
// The node token is the credential a node presents to GetNodeByToken over
// gRPC, and grpc/server.go plus acl_reconciler.go each keep their own
// tokenPrefix helper so a node is only ever named by its first 8 characters.
// SendCommand's ACL warning printed the raw token, and Core's logs are shipped
// off the box, so a transient Redis error wrote a live node credential into the
// log pipeline.
func TestSendCommand_DoesNotLogTheWholeNodeToken(t *testing.T) {
	const token = "nodetok-3f9a2c7e11b4d8065ae2f1c39d7b0a4e"

	rdb := newQueueTestRedis(t)
	q := NewQueueService(rdb)
	q.SetACL(redisacl.NewHandshake(aclFailingStore{}, nil, "cluster-secret"))

	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(prev)

	// "create" is one of the ACL-relevant actions, so the ensure path runs.
	if err := q.SendCommand(context.Background(), token, "create", map[string]string{}, nil); err != nil {
		t.Fatalf("SendCommand: %v", err)
	}

	out := logged.String()
	if !strings.Contains(out, "pre-placement ACL ensure failed") {
		t.Fatalf("the warning under test never fired; log was: %q", out)
	}
	if strings.Contains(out, token) {
		t.Errorf("the full node token was logged:\n%s", out)
	}
	if !strings.Contains(out, tokenPrefix(token)) {
		t.Errorf("the log no longer identifies the node at all; want the %q prefix:\n%s", tokenPrefix(token), out)
	}
}

// TestTokenPrefixTruncates is the unit-level half: the helper must actually cut
// the token down, and must not panic on a short one.
func TestTokenPrefixTruncates(t *testing.T) {
	long := "0123456789abcdef"
	if got := tokenPrefix(long); got != "01234567" {
		t.Errorf("tokenPrefix(%q) = %q, want %q", long, got, "01234567")
	}
	if got := tokenPrefix("abc"); got != "abc" {
		t.Errorf("tokenPrefix(%q) = %q, want it returned unchanged", "abc", got)
	}
}
