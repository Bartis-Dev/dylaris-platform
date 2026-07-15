package main

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/time/rate"
)

// TestProjectID pins CRC32 (IEEE) determinism for the quota project-ID
// derivation. A collision here would mix quota tracking across tenants, so
// these are golden vectors, not values re-derived through the function under
// test's own algorithm.
func TestProjectID(t *testing.T) {
	cases := []struct {
		uuid string
		want uint32
	}{
		{"11111111-1111-1111-1111-111111111111", 452356829},
		{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", 1438003775},
		{"", 0},
	}
	for _, c := range cases {
		if got := projectID(c.uuid); got != c.want {
			t.Errorf("projectID(%q) = %d, want %d", c.uuid, got, c.want)
		}
	}

	t.Run("deterministic across calls", func(t *testing.T) {
		u := "some-server-uuid-1234"
		if projectID(u) != projectID(u) {
			t.Fatal("projectID is not deterministic")
		}
	})

	t.Run("different uuid yields different id", func(t *testing.T) {
		a := projectID("server-a")
		b := projectID("server-b")
		if a == b {
			t.Fatalf("projectID collided for distinct uuids: %d", a)
		}
	})
}

// TestMakeLimiter pins the burst clamp: <=0 disables the limiter entirely
// (nil, meaning unlimited), otherwise burst is bytesPerSec clamped into
// [64KiB, 256KiB].
func TestMakeLimiter(t *testing.T) {
	const (
		floor   = 64 * 1024
		ceiling = 256 * 1024
	)

	t.Run("zero disables the limiter", func(t *testing.T) {
		if got := makeLimiter(0); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	t.Run("negative disables the limiter", func(t *testing.T) {
		if got := makeLimiter(-5); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})

	cases := []struct {
		name        string
		bytesPerSec int64
		wantBurst   int
	}{
		{"below floor clamps up to 64KiB", 1, floor},
		{"exactly at floor (64KiB)", floor, floor},
		{"mid-range unclamped", 100000, 100000},
		{"exactly at ceiling (256KiB)", ceiling, ceiling},
		{"above ceiling clamps down to 256KiB", 500000, ceiling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lim := makeLimiter(c.bytesPerSec)
			if lim == nil {
				t.Fatal("got nil limiter, want a real limiter")
			}
			if lim.Burst() != c.wantBurst {
				t.Fatalf("Burst() = %d, want %d", lim.Burst(), c.wantBurst)
			}
			if lim.Limit() != rate.Limit(c.bytesPerSec) {
				t.Fatalf("Limit() = %v, want %v", lim.Limit(), rate.Limit(c.bytesPerSec))
			}
		})
	}
}

// TestRconPacketRoundTrip round-trips writeRconPacket/readRconPacket over a
// bytes.Buffer (both take an io.Writer / io.Reader, so no live net.Conn is
// needed for the framing logic itself).
func TestRconPacketRoundTrip(t *testing.T) {
	t.Run("normal body round-trips id/type/body", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeRconPacket(&buf, 5, rconTypeExec, "say hello"); err != nil {
			t.Fatalf("write: %v", err)
		}
		id, typ, body, err := readRconPacket(&buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if id != 5 || typ != rconTypeExec || body != "say hello" {
			t.Fatalf("got id=%d typ=%d body=%q, want id=5 typ=%d body=%q", id, typ, body, rconTypeExec, "say hello")
		}
	})

	t.Run("empty body round-trips", func(t *testing.T) {
		var buf bytes.Buffer
		if err := writeRconPacket(&buf, 1, rconTypeAuth, ""); err != nil {
			t.Fatalf("write: %v", err)
		}
		id, typ, body, err := readRconPacket(&buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if id != 1 || typ != rconTypeAuth || body != "" {
			t.Fatalf("got id=%d typ=%d body=%q, want id=1 typ=%d body=empty", id, typ, body, rconTypeAuth)
		}
	})

	t.Run("body over the max length is rejected before writing", func(t *testing.T) {
		oversized := strings.Repeat("x", rconMaxBodyLen+1)
		var buf bytes.Buffer
		if err := writeRconPacket(&buf, 1, rconTypeExec, oversized); err == nil {
			t.Fatal("expected error for oversized body, got nil")
		}
		if buf.Len() != 0 {
			t.Fatalf("expected nothing written on rejected oversized body, got %d bytes", buf.Len())
		}
	})
}
