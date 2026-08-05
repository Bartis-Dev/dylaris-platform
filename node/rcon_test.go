package main

import (
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// rconPkt is one packet as the fake server saw it.
type rconPkt struct {
	id   int32
	typ  int32
	body string
}

// fakeRconServer speaks enough of the protocol to exercise the client, and
// records every request it receives so a test can assert what was SENT. It
// mirrors Minecraft in the one way that matters here: any request that is not
// AUTH(3) or EXECCOMMAND(2) makes it hang up, which is what broke every command.
type fakeRconServer struct {
	ln       net.Listener
	replies  []string // reply packets sent for an EXECCOMMAND, in order
	mu       sync.Mutex
	received []rconPkt
	hungUp   bool
}

func startFakeRcon(t *testing.T, replies []string) *fakeRconServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeRconServer{ln: ln, replies: replies}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeRconServer) addr() string { return s.ln.Addr().String() }

func (s *fakeRconServer) got() []rconPkt {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rconPkt(nil), s.received...)
}

func (s *fakeRconServer) serve() {
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	for {
		id, typ, body, err := readRconPacket(conn)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.received = append(s.received, rconPkt{id, typ, body})
		s.mu.Unlock()

		switch typ {
		case rconTypeAuth:
			_ = writeRconPacket(conn, id, rconTypeAuthResp, "")
		case rconTypeExec:
			for _, r := range s.replies {
				_ = writeRconPacket(conn, id, rconTypeRespValue, r)
			}
		default:
			// What Minecraft does with anything else, and the whole point.
			s.mu.Lock()
			s.hungUp = true
			s.mu.Unlock()
			return
		}
	}
}

// TestExecRconNeverSendsASentinel is the regression guard. The client used to
// send a RESPONSE_VALUE(0) request after the exec as a Valve-style end-of-reply
// marker. Minecraft accepts only AUTH(3) and EXECCOMMAND(2) as requests and
// closes the connection on anything else, so every single command came back
// "read exec resp: EOF" - live player management, whitelist, operators and the
// Scheduled Tasks "say" jobs were all dead.
func TestExecRconNeverSendsASentinel(t *testing.T) {
	srv := startFakeRcon(t, []string{"There are 0 of a max of 20 players online: "})

	out, err := execRcon(srv.addr(), "pw", "list", time.Second)
	if err != nil {
		t.Fatalf("execRcon: %v", err)
	}
	if out != "There are 0 of a max of 20 players online:" {
		t.Errorf("out = %q", out)
	}

	for i, p := range srv.got() {
		if p.typ != rconTypeAuth && p.typ != rconTypeExec {
			t.Errorf("packet %d has type %d; Minecraft accepts only AUTH(%d) and EXECCOMMAND(%d) and hangs up on the rest",
				i, p.typ, rconTypeAuth, rconTypeExec)
		}
	}
	if srv.hungUp {
		t.Error("the server hung up on an unsupported request type")
	}
	if n := len(srv.got()); n != 2 {
		t.Errorf("sent %d packets, want exactly 2 (auth + exec)", n)
	}
}

// A long reply arrives split across packets; all of them belong to the answer.
func TestExecRconAssemblesASplitReply(t *testing.T) {
	srv := startFakeRcon(t, []string{"part one ", "part two ", "part three"})

	out, err := execRcon(srv.addr(), "pw", "list", time.Second)
	if err != nil {
		t.Fatalf("execRcon: %v", err)
	}
	if out != "part one part two part three" {
		t.Errorf("out = %q, want the three parts joined", out)
	}
}

// A command that produces no output must not hang for the full timeout and must
// not be reported as a failure.
func TestExecRconHandlesAnEmptyReply(t *testing.T) {
	srv := startFakeRcon(t, []string{""})

	start := time.Now()
	out, err := execRcon(srv.addr(), "pw", "say hi", 5*time.Second)
	if err != nil {
		t.Fatalf("execRcon: %v", err)
	}
	if out != "" {
		t.Errorf("out = %q, want empty", out)
	}
	// The idle gap, not the caller's 5s timeout.
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v; an empty reply should end on the idle gap, not the full timeout", d)
	}
}

// A wrong password is reported as such rather than as a transport error.
func TestExecRconReportsABadPassword(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, _, err := readRconPacket(conn); err != nil {
			return
		}
		// Minecraft signals a bad password with request id -1.
		_ = writeRconPacket(conn, -1, rconTypeAuthResp, "")
	}()

	if _, err := execRcon(ln.Addr().String(), "wrong", "list", time.Second); err == nil ||
		!strings.Contains(err.Error(), "auth failed") {
		t.Errorf("err = %v, want an auth failure", err)
	}
}

// Guards the wire format against a silent change: the size header counts
// everything after itself, and the packet ends in two nulls.
func TestWriteRconPacketWireFormat(t *testing.T) {
	var buf strings.Builder
	if err := writeRconPacket(&stringWriter{&buf}, 7, rconTypeExec, "list"); err != nil {
		t.Fatalf("write: %v", err)
	}
	b := []byte(buf.String())
	if len(b) != 18 {
		t.Fatalf("wrote %d bytes, want 18 (4 size + 4 id + 4 type + 4 body + 2 nulls)", len(b))
	}
	if size := binary.LittleEndian.Uint32(b[0:4]); size != 14 {
		t.Errorf("size header = %d, want 14 (everything after the header)", size)
	}
	if id := binary.LittleEndian.Uint32(b[4:8]); id != 7 {
		t.Errorf("id = %d, want 7", id)
	}
	if typ := binary.LittleEndian.Uint32(b[8:12]); typ != rconTypeExec {
		t.Errorf("type = %d, want %d", typ, rconTypeExec)
	}
	if string(b[12:16]) != "list" {
		t.Errorf("body = %q, want %q", b[12:16], "list")
	}
	if b[16] != 0 || b[17] != 0 {
		t.Errorf("trailing bytes = %v, want two nulls", b[16:18])
	}
}

type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

var _ io.Writer = (*stringWriter)(nil)
