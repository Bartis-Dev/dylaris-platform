package main

// Phase 9 — minimal RCON client. Valve-spec source-engine RCON, 4-byte LE
// header (request_id, type, length), null-terminated payload + 1 byte pad.
// We support exactly two packet types: SERVERDATA_AUTH (3) and
// SERVERDATA_EXECCOMMAND (2). Response types are SERVERDATA_AUTH_RESPONSE
// (2) and SERVERDATA_RESPONSE_VALUE (0).
//
// One connection per command — no pooling V1. RCON is request-response and
// user-driven; pooling adds complexity without saving meaningful latency at
// human-typing frequencies.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

const (
	rconTypeAuth        = 3
	rconTypeAuthResp    = 2
	rconTypeExec        = 2
	rconTypeRespValue   = 0
	rconRequestExec     = 1
	rconRequestAuth     = 2
	rconMaxBodyLen      = 4096
	rconDefaultPortVar  = 25575
	rconDefaultTimeout  = 3 * time.Second
)

// execRcon dials addr, auths, runs cmd, returns the server's reply.
//
// Implementation notes:
//   * MC servers may split long replies into multiple RESPONSE_VALUE packets.
//     We send a small SERVERDATA_RESPONSE_VALUE (type=2 actually, the
//     "request" type) as a sentinel right after the exec — the server
//     echoes it back AFTER any reply chunks, telling us we're done.
//     This is the standard trick documented in the Valve wiki.
//   * If the server only sends one reply we still terminate via timeout.
func execRcon(addr, password, command string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = rconDefaultTimeout
	}
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "", fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// --- Auth ---
	if err := writeRconPacket(conn, rconRequestAuth, rconTypeAuth, password); err != nil {
		return "", fmt.Errorf("write auth: %w", err)
	}
	id, typ, _, err := readRconPacket(conn)
	if err != nil {
		return "", fmt.Errorf("read auth resp: %w", err)
	}
	// Some implementations send an empty RESPONSE_VALUE before the AUTH_RESPONSE.
	if typ == rconTypeRespValue {
		id, typ, _, err = readRconPacket(conn)
		if err != nil {
			return "", fmt.Errorf("read auth resp (2nd): %w", err)
		}
	}
	if id == -1 {
		return "", errors.New("rcon auth failed (bad password)")
	}
	if typ != rconTypeAuthResp {
		return "", fmt.Errorf("unexpected auth response type %d", typ)
	}

	// --- Exec ---
	if err := writeRconPacket(conn, rconRequestExec, rconTypeExec, command); err != nil {
		return "", fmt.Errorf("write exec: %w", err)
	}
	// Sentinel: a second RESPONSE_VALUE packet whose echo signals end-of-reply.
	const sentinelID = rconRequestExec + 1
	if err := writeRconPacket(conn, sentinelID, rconTypeRespValue, ""); err != nil {
		return "", fmt.Errorf("write sentinel: %w", err)
	}

	var out strings.Builder
	for {
		rid, rtyp, body, err := readRconPacket(conn)
		if err != nil {
			// If we already collected output, treat read-timeout as "done".
			if out.Len() > 0 {
				break
			}
			return "", fmt.Errorf("read exec resp: %w", err)
		}
		if rtyp != rconTypeRespValue {
			continue
		}
		// Sentinel echo → finished.
		if rid == sentinelID {
			break
		}
		out.WriteString(body)
	}
	return strings.TrimRight(out.String(), "\x00 \r\n"), nil
}

func writeRconPacket(w io.Writer, id int32, typ int32, body string) error {
	if len(body) > rconMaxBodyLen {
		return fmt.Errorf("body too large (%d)", len(body))
	}
	// payload: id(4) + type(4) + body + 1 null + 1 trailing null = body+10
	payloadLen := int32(4 + 4 + len(body) + 2)
	buf := make([]byte, 4+payloadLen) // +4 for size header
	binary.LittleEndian.PutUint32(buf[0:4], uint32(payloadLen))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(id))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(typ))
	copy(buf[12:], body)
	// Last two bytes already 0 (Go default).
	_, err := w.Write(buf)
	return err
}

func readRconPacket(r io.Reader) (int32, int32, string, error) {
	var sizeBuf [4]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return 0, 0, "", err
	}
	size := int32(binary.LittleEndian.Uint32(sizeBuf[:]))
	if size < 10 || size > rconMaxBodyLen+10 {
		return 0, 0, "", fmt.Errorf("invalid packet size %d", size)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, 0, "", err
	}
	id := int32(binary.LittleEndian.Uint32(buf[0:4]))
	typ := int32(binary.LittleEndian.Uint32(buf[4:8]))
	// Body is everything between offset 8 and the trailing two null bytes.
	body := string(buf[8 : size-2])
	return id, typ, body, nil
}
