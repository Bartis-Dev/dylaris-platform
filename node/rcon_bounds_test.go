package main

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// rconFlooder answers the auth handshake and then streams RESPONSE_VALUE
// packets back to back until the client hangs up, i.e. exactly what a hostile
// or merely very chatty listener on the RCON port does. That port is inside the
// tenant's own container: they choose it (rcon.port in server.properties, mirrored
// into the panel's RCON config) and they choose what listens on it.
func rconFlooder(t *testing.T, body string) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lis.Close() })

	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, _, err := readRconPacket(conn); err != nil { // auth
			return
		}
		if err := writeRconPacket(conn, rconRequestAuth, rconTypeAuthResp, ""); err != nil {
			return
		}
		if _, _, _, err := readRconPacket(conn); err != nil { // exec
			return
		}
		for {
			if err := writeRconPacket(conn, rconRequestExec, rconTypeRespValue, body); err != nil {
				return
			}
		}
	}()
	return lis.Addr().String()
}

// Two independent bounds have to hold, and the loop had neither: it resets the
// read deadline on every packet (so it never ends while packets keep arriving)
// and appends every body to one builder (so memory grows with them).
func TestExecRconBoundsAFloodingPeer(t *testing.T) {
	addr := rconFlooder(t, strings.Repeat("A", rconMaxBodyLen))

	start := time.Now()
	out, err := execRcon(addr, "pw", "list", 500*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("execRcon returned an error rather than the reply it did receive: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("execRcon ran for %v: a peer that keeps sending never ends the read loop", elapsed)
	}
	if len(out) > rconMaxOutputLen+rconMaxBodyLen+len(rconTruncatedNote) {
		t.Errorf("execRcon assembled %d bytes; the cap is %d", len(out), rconMaxOutputLen)
	}
}

// The assembled reply crosses to Core as ONE NodeMessage, and Core's gRPC
// server refuses a received message over 128KB - which does not fail just that
// message, it ends the bidi stream and every other operation riding it.
func TestExecRconOutputFitsTheMeshMessageLimit(t *testing.T) {
	addr := rconFlooder(t, strings.Repeat("A", rconMaxBodyLen))
	out, err := execRcon(addr, "pw", "list", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	const coreMaxRecvMsgSize = 128 * 1024 // core/grpc/server.go
	if len(out) >= coreMaxRecvMsgSize {
		t.Errorf("a %d byte reply exceeds Core's %d byte receive limit and would drop the node's mesh stream",
			len(out), coreMaxRecvMsgSize)
	}
}

// The reply lands in a proto3 string field, which must be valid UTF-8 or the
// marshal fails. Truncating a stream of packets can cut a rune in half, and the
// bytes are the tenant's to choose in the first place.
func TestExecRconReturnsValidUTF8(t *testing.T) {
	// A 4096-byte body of 3-byte runes: 4096 is not divisible by 3, so every
	// packet boundary lands mid-rune.
	body := strings.Repeat("€", rconMaxBodyLen/3) + "€"[:1]
	addr := rconFlooder(t, body)

	out, err := execRcon(addr, "pw", "list", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(out) {
		t.Error("execRcon returned invalid UTF-8; marshalling it into RconExecResp.Output would fail")
	}
}

// A well-behaved server answering once still returns promptly and in full: the
// bounds must not cost the ordinary case anything.
func TestExecRconStillReturnsAShortReply(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		readRconPacket(conn)
		writeRconPacket(conn, rconRequestAuth, rconTypeAuthResp, "")
		readRconPacket(conn)
		writeRconPacket(conn, rconRequestExec, rconTypeRespValue, "There are 2 of a max of 20 players online")
		io.Copy(io.Discard, conn)
	}()

	out, err := execRcon(lis.Addr().String(), "pw", "list", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out != "There are 2 of a max of 20 players online" {
		t.Errorf("got %q", out)
	}
}
