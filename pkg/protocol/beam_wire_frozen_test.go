package protocol

import (
	"bytes"
	"strings"
	"testing"
)

// TestWriteBeamHeader_WireFormatIsFrozen pins the [0x03][len u16 BE][ticket]
// framing to LITERAL bytes.
//
// CLAUDE.md declares this a frozen cross-repo contract: platform/beam/app writes
// the header, gateway/beam/relay reads it, each repo compiles its own copy of
// header.go, and there is no version byte - so a framing change on one side
// breaks the other silently, at runtime, in production.
//
// Nothing enforced that. The pre-existing round-trip test writes with
// WriteBeamHeader and reads with ReadBeamHeader from the SAME file, so it stays
// green through any change both functions make together: uint32 instead of
// uint16, little-endian instead of big-endian, a length prefix that counts
// runes. Those are exactly the changes that would break the relay.
//
// Golden bytes here, and the mirror image in gateway/pkg/protocol, are what
// actually freeze it. If this test fails, the wire format changed: either revert
// it or add the version byte and update BOTH repos in lockstep.
func TestWriteBeamHeader_WireFormatIsFrozen(t *testing.T) {
	t.Run("short ticket", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteBeamHeader(&buf, "abc"); err != nil {
			t.Fatalf("WriteBeamHeader: %v", err)
		}
		want := []byte{0x03, 0x00, 0x03, 'a', 'b', 'c'}
		if got := buf.Bytes(); !bytes.Equal(got, want) {
			t.Errorf("wire bytes = % x, want % x", got, want)
		}
	})

	// 258 bytes is the discriminating length: big-endian writes 0x01 0x02,
	// little-endian would write 0x02 0x01. A round-trip test cannot tell those
	// apart; the relay can.
	t.Run("length is big-endian", func(t *testing.T) {
		ticket := strings.Repeat("x", 258)
		var buf bytes.Buffer
		if err := WriteBeamHeader(&buf, ticket); err != nil {
			t.Fatalf("WriteBeamHeader: %v", err)
		}
		got := buf.Bytes()
		if len(got) != 3+258 {
			t.Fatalf("header+ticket is %d bytes, want %d", len(got), 3+258)
		}
		if got[0] != 0x03 {
			t.Errorf("type byte = 0x%02x, want 0x03", got[0])
		}
		if got[1] != 0x01 || got[2] != 0x02 {
			t.Errorf("length bytes = 0x%02x 0x%02x, want 0x01 0x02 (big-endian 258)", got[1], got[2])
		}
	})

	// The cap is part of the contract too: the length field is two bytes, so a
	// ticket over 65535 must be refused rather than truncated into a frame the
	// relay would misparse.
	//
	// Only the error is asserted. The type byte is written before the length is
	// checked, so an oversized ticket does leave one stray byte on the writer -
	// deliberately not treated as a defect: the sole caller
	// (beam/app/relay_client.go) closes the connection on this error, so the
	// byte reaches no reader. Tightening a FROZEN contract file for a case that
	// cannot happen is the kind of defensive change this repo declines.
	t.Run("oversized ticket is refused", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteBeamHeader(&buf, strings.Repeat("x", 65536)); err == nil {
			t.Fatal("a 65536-byte ticket was accepted; its length cannot fit the two-byte field")
		}
	})
}
