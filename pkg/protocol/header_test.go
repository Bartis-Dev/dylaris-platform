package protocol

import (
	"bytes"
	"testing"
)

func TestWriteReadToken(t *testing.T) {
	var buf bytes.Buffer
	token := "my-secret-token-abc123"
	if err := WriteToken(&buf, token); err != nil {
		t.Fatal(err)
	}
	got, err := ReadToken(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Errorf("got %q, want %q", got, token)
	}
}

func TestWriteStreamHeaderNew_RoundTrip(t *testing.T) {
	sessionID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	var buf bytes.Buffer
	if err := WriteStreamHeaderNew(&buf, sessionID, "192.168.1.1", 25565); err != nil {
		t.Fatal(err)
	}
	streamType, gotID, ip, port, _, err := ReadStreamHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if streamType != StreamTypeNew {
		t.Errorf("streamType: got %d, want %d", streamType, StreamTypeNew)
	}
	if gotID != sessionID {
		t.Error("session ID mismatch")
	}
	if ip != "192.168.1.1" {
		t.Errorf("ip: got %q, want %q", ip, "192.168.1.1")
	}
	if port != 25565 {
		t.Errorf("port: got %d, want %d", port, 25565)
	}
}

func TestWriteStreamHeaderResume_RoundTrip(t *testing.T) {
	sessionID := [16]byte{16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	var buf bytes.Buffer
	if err := WriteStreamHeaderResume(&buf, sessionID, 42000); err != nil {
		t.Fatal(err)
	}
	streamType, gotID, _, _, bytesReceived, err := ReadStreamHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if streamType != StreamTypeResume {
		t.Errorf("streamType: got %d, want %d", streamType, StreamTypeResume)
	}
	if gotID != sessionID {
		t.Error("session ID mismatch")
	}
	if bytesReceived != 42000 {
		t.Errorf("bytesReceived: got %d, want %d", bytesReceived, 42000)
	}
}

func TestWriteReadBeamHeader_RoundTrip(t *testing.T) {
	ticket := "eyJhbGciOiJIUzI1NiJ9.testpayload.sig"
	var buf bytes.Buffer
	if err := WriteBeamHeader(&buf, ticket); err != nil {
		t.Fatal(err)
	}
	// WriteBeamHeader writes [0x03][len u16][ticket bytes]
	// Read and verify the type byte first, then parse the rest with ReadBeamHeader
	typeByte := make([]byte, 1)
	if _, err := buf.Read(typeByte); err != nil {
		t.Fatal(err)
	}
	if typeByte[0] != StreamTypeBeam {
		t.Errorf("expected StreamTypeBeam (0x%02x), got 0x%02x", StreamTypeBeam, typeByte[0])
	}
	got, err := ReadBeamHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got != ticket {
		t.Errorf("got %q, want %q", got, ticket)
	}
}

func TestWriteBeamHeader_TooLong(t *testing.T) {
	// 65536 bytes > max uint16 (65535)
	oversized := string(make([]byte, 65536))
	var buf bytes.Buffer
	if err := WriteBeamHeader(&buf, oversized); err == nil {
		t.Fatal("expected error for ticket exceeding 65535 bytes")
	}
}

func TestReadStreamHeader_UnknownType(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(0xFF)          // invalid type byte
	buf.Write(make([]byte, 16)) // session ID bytes (to avoid EOF before type check)
	_, _, _, _, _, err := ReadStreamHeader(&buf)
	if err == nil {
		t.Fatal("expected error for unknown stream type 0xFF")
	}
}

func TestGenerateSessionID_NonZero(t *testing.T) {
	id := GenerateSessionID()
	var zero [16]byte
	if id == zero {
		t.Fatal("expected non-zero session ID, got all-zero")
	}
}

func TestGenerateSessionID_Unique(t *testing.T) {
	id1 := GenerateSessionID()
	id2 := GenerateSessionID()
	if id1 == id2 {
		t.Fatal("two consecutive session IDs should differ (random source)")
	}
}
