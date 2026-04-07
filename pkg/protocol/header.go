package protocol

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// --- Handshake / Auth (v1) ---

// ReadToken reads the auth token (String + Newline) from the stream.
// This is used by the Gate to authenticate the Link.
func ReadToken(r io.Reader) (string, error) {
	// We use bufio for efficient reading until the newline
	reader := bufio.NewReader(r)
	token, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	// Remove whitespace (like \n or \r)
	return strings.TrimSpace(token), nil
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
