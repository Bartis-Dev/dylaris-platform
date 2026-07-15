package protocol

import (
	"bytes"
	"testing"
)

// These tests cover truncated-input error paths only. They do not touch the
// wire format itself (byte layout / ordering is unchanged) - they just feed
// deliberately short buffers into the existing Read* functions and assert an
// error comes back instead of a panic or garbage value. Happy-path
// round-trips already live in header_test.go.

func TestReadStreamHeader_Truncated(t *testing.T) {
	fullSessionID := make([]byte, 16)
	for i := range fullSessionID {
		fullSessionID[i] = byte(i + 1)
	}

	tests := []struct {
		name string
		buf  []byte
	}{
		{
			name: "empty reader (no type byte)",
			buf:  []byte{},
		},
		{
			name: "type byte only, no session ID",
			buf:  []byte{StreamTypeNew},
		},
		{
			name: "type byte + partial session ID",
			buf:  append([]byte{StreamTypeNew}, fullSessionID[:8]...),
		},
		{
			name: "new: session ID complete, missing IP length byte",
			buf:  append([]byte{StreamTypeNew}, fullSessionID...),
		},
		{
			name: "new: IP length declared but IP bytes missing",
			buf:  append(append([]byte{StreamTypeNew}, fullSessionID...), 5 /* ipLen */),
		},
		{
			name: "new: IP length declared, IP bytes short",
			buf:  append(append([]byte{StreamTypeNew}, fullSessionID...), append([]byte{5}, []byte("ab")...)...),
		},
		{
			name: "new: full IP present but port bytes missing",
			buf:  append(append([]byte{StreamTypeNew}, fullSessionID...), append([]byte{3}, []byte("abc")...)...),
		},
		{
			name: "new: port byte truncated (only 1 of 2 bytes)",
			buf:  append(append(append([]byte{StreamTypeNew}, fullSessionID...), append([]byte{3}, []byte("abc")...)...), 0x63),
		},
		{
			name: "resume: session ID complete, missing bytesReceived",
			buf:  append([]byte{StreamTypeResume}, fullSessionID...),
		},
		{
			name: "resume: bytesReceived truncated (4 of 8 bytes)",
			buf:  append(append([]byte{StreamTypeResume}, fullSessionID...), 0x00, 0x00, 0x00, 0x01),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.buf)
			_, _, _, _, _, err := ReadStreamHeader(r)
			if err == nil {
				t.Fatalf("ReadStreamHeader: expected error for truncated input %v, got nil", tt.buf)
			}
		})
	}
}

func TestReadBeamHeader_Truncated(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
	}{
		{
			name: "empty reader (no length prefix)",
			buf:  []byte{},
		},
		{
			name: "length prefix truncated (1 of 2 bytes)",
			buf:  []byte{0x00},
		},
		{
			name: "length prefix exceeds available ticket bytes",
			// Declares a 10-byte ticket but only supplies 3.
			buf: append([]byte{0x00, 0x0a}, []byte("abc")...),
		},
		{
			name: "length prefix non-zero, zero ticket bytes supplied",
			buf:  []byte{0x00, 0x05},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.buf)
			_, err := ReadBeamHeader(r)
			if err == nil {
				t.Fatalf("ReadBeamHeader: expected error for truncated input %v, got nil", tt.buf)
			}
		})
	}
}
