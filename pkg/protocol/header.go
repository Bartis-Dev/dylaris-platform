package protocol

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// --- Handshake / Auth ---

// maxTokenLen bounds the byte-by-byte handshake read so a misbehaving or
// malicious peer can't stream unbounded bytes before sending a newline.
const maxTokenLen = 4096

// ReadToken reads the auth token (String + Newline) from the stream.
// This is used by the Gate to authenticate the Link.
//
// It reads one byte at a time directly from r and stops at the first '\n'.
// A buffered reader (bufio) must NOT be used here: it can read ahead past the
// newline and buffer bytes that belong to the next protocol phase (the yamux
// preface), which would then be silently dropped when this function returns —
// corrupting the session ("error reading server preface: EOF"). Reading the
// raw conn byte-by-byte leaves any post-newline bytes untouched on the conn
// for yamux.Server/Client to consume.
func ReadToken(r io.Reader) (string, error) {
	var b [1]byte
	buf := make([]byte, 0, 64)
	for {
		n, err := r.Read(b[:])
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			buf = append(buf, b[0])
			if len(buf) > maxTokenLen {
				return "", fmt.Errorf("token exceeds %d bytes without newline", maxTokenLen)
			}
		}
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				break
			}
			return "", err
		}
	}
	// Remove whitespace (like \r)
	return strings.TrimSpace(string(buf)), nil
}

// WriteToken writes the token (String + Newline) to the stream.
// This is used by the Link to authenticate.
func WriteToken(w io.Writer, token string) error {
	_, err := w.Write([]byte(token + "\n"))
	return err
}

// --- Stream Protocol (v5) ---
// New:    [0x01] [SESSION_ID (16 bytes)] [IP_LEN (1 byte)] [IP_STRING (n bytes)] [PORT (2 bytes)]
// Resume: [0x02] [SESSION_ID (16 bytes)] [BYTES_RECEIVED (8 bytes, big-endian)]

const (
	StreamTypeNew    byte = 0x01
	StreamTypeResume byte = 0x02
	StreamTypeBeam   byte = 0x03
)

// GenerateSessionID creates a random 16-byte session identifier.
func GenerateSessionID() [16]byte {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return id
}

// WriteStreamHeaderNew writes a new-stream header with session ID and target address.
func WriteStreamHeaderNew(w io.Writer, sessionID [16]byte, ip string, port uint16) error {
	// 1. Type
	if _, err := w.Write([]byte{StreamTypeNew}); err != nil {
		return err
	}
	// 2. Session ID
	if _, err := w.Write(sessionID[:]); err != nil {
		return err
	}
	// 3. IP length (Max 255 chars)
	if len(ip) > 255 {
		ip = ip[:255]
	}
	if err := binary.Write(w, binary.BigEndian, uint8(len(ip))); err != nil {
		return err
	}
	// 4. IP String
	if _, err := w.Write([]byte(ip)); err != nil {
		return err
	}
	// 5. Port
	return binary.Write(w, binary.BigEndian, port)
}

// WriteStreamHeaderResume writes a resume-stream header with session ID and byte position.
// bytesReceived tells the Link how many bytes the edge actually delivered to the player,
// so the Link can replay any in-flight data that was lost when the gate died.
func WriteStreamHeaderResume(w io.Writer, sessionID [16]byte, bytesReceived uint64) error {
	// 1. Type
	if _, err := w.Write([]byte{StreamTypeResume}); err != nil {
		return err
	}
	// 2. Session ID
	if _, err := w.Write(sessionID[:]); err != nil {
		return err
	}
	// 3. Bytes received by edge
	return binary.Write(w, binary.BigEndian, bytesReceived)
}

// FROZEN CROSS-REPO CONTRACT: the Beam stream header below is a wire contract
// between two SEPARATE repos. The beam desktop app (platform/beam/app, which
// WRITES this header via WriteBeamHeader) and the beam-relay (gateway/beam/relay,
// which READS it via ReadBeamHeader) each compile their OWN copy of this file
// (platform/pkg/protocol vs gateway/pkg/protocol). There is no version byte, so a
// change to this framing on one side silently breaks the other. Do NOT change the
// [0x03][len][ticket] layout without updating BOTH repos in lockstep (add a version
// byte first if the format must evolve). Same discipline as the edge<->splice
// stream header.
//
// WriteBeamHeader writes a Beam stream header with the JWT ticket.
// Format: [0x03] [TICKET_LEN (2 bytes, big-endian)] [TICKET (n bytes)]
func WriteBeamHeader(w io.Writer, ticket string) error {
	if _, err := w.Write([]byte{StreamTypeBeam}); err != nil {
		return err
	}
	ticketBytes := []byte(ticket)
	if len(ticketBytes) > 65535 {
		return fmt.Errorf("ticket too long: %d bytes (max 65535)", len(ticketBytes))
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(ticketBytes))); err != nil {
		return err
	}
	_, err := w.Write(ticketBytes)
	return err
}

// ReadBeamHeader reads a Beam stream header and returns the JWT ticket.
// Assumes the type byte (0x03) has already been read.
func ReadBeamHeader(r io.Reader) (string, error) {
	var ticketLen uint16
	if err := binary.Read(r, binary.BigEndian, &ticketLen); err != nil {
		return "", err
	}
	ticketBytes := make([]byte, ticketLen)
	if _, err := io.ReadFull(r, ticketBytes); err != nil {
		return "", err
	}
	return string(ticketBytes), nil
}

// ReadStreamHeader reads a stream header and returns the type, session ID, target address, and byte position.
// For StreamTypeNew: returns sessionID, ip, and port (bytesReceived=0).
// For StreamTypeResume: returns sessionID and bytesReceived (ip="" and port=0).
func ReadStreamHeader(r io.Reader) (streamType byte, sessionID [16]byte, ip string, port uint16, bytesReceived uint64, err error) {
	// 1. Read type byte
	var typeBuf [1]byte
	if _, err = io.ReadFull(r, typeBuf[:]); err != nil {
		return
	}
	streamType = typeBuf[0]

	// 2. Read session ID (both types)
	if _, err = io.ReadFull(r, sessionID[:]); err != nil {
		return
	}

	// 3. For new streams, read target address
	if streamType == StreamTypeNew {
		var ipLen uint8
		if err = binary.Read(r, binary.BigEndian, &ipLen); err != nil {
			return
		}
		ipBytes := make([]byte, ipLen)
		if _, err = io.ReadFull(r, ipBytes); err != nil {
			return
		}
		ip = string(ipBytes)
		err = binary.Read(r, binary.BigEndian, &port)
		return
	}

	// 4. For resume streams, read bytes received by edge
	if streamType == StreamTypeResume {
		err = binary.Read(r, binary.BigEndian, &bytesReceived)
		return
	}

	err = fmt.Errorf("unknown stream type: 0x%02x", streamType)
	return
}
