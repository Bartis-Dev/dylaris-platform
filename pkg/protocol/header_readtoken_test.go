package protocol

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// The two properties that make ReadToken read the connection ONE BYTE AT A
// TIME. Neither repo tested them, which is how the platform copy sat on a bufio
// implementation - taken before the gateway fixed this - carrying both defects
// its own comment described.
//
// They are tested here rather than trusted to the comment because the function
// is EXPORTED: staticcheck's U1000 does not flag it, so a caller could appear in
// this repo with nothing objecting.

// A buffered reader fills its buffer from the connection, so it swallows the
// bytes after the newline. Those bytes belong to the NEXT protocol phase - the
// yamux preface - and losing them surfaces as "error reading server preface:
// EOF" on a session that authenticated perfectly.
func TestReadTokenLeavesTheBytesAfterTheNewlineOnTheReader(t *testing.T) {
	const preface = "\x00\x00\x00yamux-preface-bytes"
	r := bytes.NewReader([]byte("a-token\n" + preface))

	got, err := ReadToken(r)
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "a-token" {
		t.Fatalf("token = %q, want %q", got, "a-token")
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read the rest: %v", err)
	}
	if string(rest) != preface {
		t.Errorf("the reader has %q left, want %q - the handshake read past the newline and "+
			"dropped bytes belonging to the next protocol phase", rest, preface)
	}
}

// Without a bound, a peer that opens a connection and never sends a newline
// streams into memory for as long as it likes.
func TestReadTokenRefusesATokenThatNeverEnds(t *testing.T) {
	// One byte over the cap, so the bound is what stops it rather than the
	// reader running out.
	flood := strings.Repeat("x", maxTokenLen+1)
	_, err := ReadToken(strings.NewReader(flood))
	if err == nil {
		t.Fatalf("a %d-byte run with no newline was accepted", len(flood))
	}
	// The error has to come from the BOUND. A reader that simply runs out
	// fails too, so asserting "some error" would pass on an implementation
	// with no bound at all - which is the one this replaced.
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error is %v, which is not the length bound refusing it", err)
	}

	// Exactly at the cap, terminated, is still a valid token: the bound must
	// refuse the flood without moving the line for a legitimate one.
	ok := strings.Repeat("x", maxTokenLen)
	got, err := ReadToken(strings.NewReader(ok + "\n"))
	if err != nil {
		t.Fatalf("a %d-byte token was refused: %v", maxTokenLen, err)
	}
	if got != ok {
		t.Errorf("token length = %d, want %d", len(got), len(ok))
	}
}

// A peer that closes after the token without sending the newline has still said
// everything it had to say.
func TestReadTokenAcceptsATokenEndedByEOF(t *testing.T) {
	got, err := ReadToken(strings.NewReader("a-token"))
	if err != nil {
		t.Fatalf("ReadToken: %v", err)
	}
	if got != "a-token" {
		t.Errorf("token = %q, want %q", got, "a-token")
	}
}
