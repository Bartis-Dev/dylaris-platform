package database

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
)

// The server replies below are VERBATIM from a live valkey 8, captured by
// pinging it as an ACL user missing +ping, with a wrong password, and with no
// credentials against a server that requires them. They are not paraphrased,
// because the classifier matches their prefixes and a prettied-up copy would
// let a real reply drift away from what the test pins.
const (
	realNoPermReply    = "NOPERM User pingless has no permissions to run the 'ping' command"
	realWrongPassReply = "WRONGPASS invalid username-password pair or user is disabled."
	realNoAuthReply    = "NOAUTH Authentication required."
)

// serverReply builds an error of the kind go-redis returns for a reply from the
// server, as opposed to a transport failure. That distinction is exactly what
// the classifier keys on, so a test that used errors.New here would be
// exercising the wrong branch.
//
// It carries only the two methods that define the interface. Adding an Is or an
// As would change how errors.Is and errors.As treat it, which is the machinery
// under test.
func serverReply(msg string) error {
	return redisServerReply(msg)
}

type redisServerReply string

func (r redisServerReply) Error() string { return string(r) }
func (r redisServerReply) RedisError()   {}

func TestClassifyRedisError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want RedisFailure
	}{
		{name: "no error", err: nil, want: RedisOK},

		{name: "acl user may not run the command", err: serverReply(realNoPermReply), want: RedisPermissionDenied},
		{name: "wrong password", err: serverReply(realWrongPassReply), want: RedisAuthFailed},
		{name: "no credentials sent", err: serverReply(realNoAuthReply), want: RedisAuthFailed},

		// The server answered, so this is not an outage however unfamiliar the
		// reply is. Reporting it as unreachable would send an operator to the
		// network when the server is right there talking to them.
		{name: "some other server reply", err: serverReply("LOADING the dataset is being loaded"), want: RedisServerError},
		{name: "key is missing", err: redis.Nil, want: RedisServerError},

		// Transport failures are recognised by TYPE, never by their text. This
		// row carries a German message on purpose: on a German Windows host a
		// refused dial really does read like this, and a classifier matching
		// "connection refused" would fall through to the wrong class.
		{
			name: "refused dial with a localised os message",
			err:  errors.New("dial tcp 127.0.0.1:1: connectex: Es konnte keine Verbindung hergestellt werden, da der Zielcomputer die Verbindung verweigerte."),
			want: RedisUnreachable,
		},
		{name: "plain network error", err: &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}, want: RedisUnreachable},
		{name: "wrapped transport failure", err: fmt.Errorf("ping: %w", errors.New("connection reset by peer")), want: RedisUnreachable},

		// Wrapping must not hide the class: go-redis errors reach callers
		// through layers that add context.
		{name: "wrapped server reply", err: fmt.Errorf("acl check: %w", serverReply(realNoPermReply)), want: RedisPermissionDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyRedisError(tt.err); got != tt.want {
				t.Fatalf("ClassifyRedisError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestNeedsOperator pins which classes are allowed to fail a container
// healthcheck. Getting this backwards is the difference between an outage that
// heals itself and one that restart-loops Core over a config mistake.
func TestNeedsOperator(t *testing.T) {
	tests := []struct {
		failure RedisFailure
		want    bool
	}{
		{failure: RedisOK, want: false},
		{failure: RedisUnreachable, want: false},
		{failure: RedisAuthFailed, want: true},
		{failure: RedisPermissionDenied, want: true},
		{failure: RedisServerError, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.failure.Slug(), func(t *testing.T) {
			if got := tt.failure.NeedsOperator(); got != tt.want {
				t.Fatalf("%s.NeedsOperator() = %v, want %v", tt.failure.Slug(), got, tt.want)
			}
		})
	}
}

// TestSlugsAreDistinct guards the panel contract. Slug is what the panel keys
// its guidance on, so two classes sharing one would silently show the wrong
// advice for one of them.
func TestSlugsAreDistinct(t *testing.T) {
	all := []RedisFailure{RedisOK, RedisUnreachable, RedisAuthFailed, RedisPermissionDenied, RedisServerError}
	seen := make(map[string]RedisFailure, len(all))
	for _, f := range all {
		slug := f.Slug()
		if slug == "" {
			t.Fatalf("failure %d has an empty slug", f)
		}
		if prev, dup := seen[slug]; dup {
			t.Fatalf("failures %d and %d share the slug %q", prev, f, slug)
		}
		seen[slug] = f
	}
}

// TestSummariesAreDistinct catches the specific regression this replaced: every
// class used to render the single string "Connection failed", which was simply
// false for the two where the connection is fine and the server answered.
func TestSummariesAreDistinct(t *testing.T) {
	all := []RedisFailure{RedisOK, RedisUnreachable, RedisAuthFailed, RedisPermissionDenied, RedisServerError}
	seen := make(map[string]RedisFailure, len(all))
	for _, f := range all {
		summary := f.Summary()
		if summary == "" {
			t.Fatalf("failure %s has an empty summary", f.Slug())
		}
		if prev, dup := seen[summary]; dup {
			t.Fatalf("failures %s and %s share the summary %q", prev.Slug(), f.Slug(), summary)
		}
		seen[summary] = f
	}
}
