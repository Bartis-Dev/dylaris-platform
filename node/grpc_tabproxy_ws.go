package main

// Node-side WebSocket bridge for custom-tab proxying (WS5). Core sends WsOpen;
// we dial the container's WS endpoint (same address order as HTTP) and pump
// frames both ways over the mesh until either side closes. Core->node frames
// arrive as separate NodeMessages routed here by request_id; container->Core
// frames are streamed from a background goroutine. Application messages are
// fragmented at wsFragmentSize so a single WsFrame never exceeds Core's
// gRPC MaxRecvMsgSize (128KB).

import (
	"log"
	"net/http"
	"sync"
	"time"

	pb "dylaris-proto/node"

	"github.com/gorilla/websocket"
)

// wsFragmentSize caps one WsFrame's data so it fits under Core's 128KB gRPC
// receive limit with envelope overhead.
const wsFragmentSize = 60 * 1024

// wsBridge tracks one open container WS for a request_id.
type wsBridge struct {
	conn      *websocket.Conn
	inbound   chan *pb.NodeMessage // Core->node WsFrame/WsClose for this request_id
	done      chan struct{}
	closeOnce sync.Once
	// reassembly buffer for Core->node fragmented application messages
	rxBuf    []byte
	rxOpcode int
}

func (b *wsBridge) close() {
	b.closeOnce.Do(func() {
		close(b.done)
		if b.conn != nil {
			b.conn.Close()
		}
	})
}

// splitWSFragments splits data into <=max byte fragments (reassembled in order
// on the far side). An empty message yields one empty fragment so it still
// transmits.
func splitWSFragments(data []byte, max int) [][]byte {
	if max <= 0 {
		max = wsFragmentSize
	}
	if len(data) == 0 {
		return [][]byte{{}}
	}
	var out [][]byte
	for off := 0; off < len(data); off += max {
		end := off + max
		if end > len(data) {
			end = len(data)
		}
		out = append(out, data[off:end])
	}
	return out
}

// handleWSOpen dials the container WS and starts the bidirectional pump.
func (m *MeshManager) handleWSOpen(cc *coreConnection, reqID, serverUUID string, open *pb.WsOpen) {
	if open == nil || open.TargetPort < 1 || open.TargetPort > 65535 {
		cc.send(errorMsg(reqID, 502, "invalid ws target"))
		return
	}
	// Forward only sanitized request headers to the container upgrade.
	hdr := http.Header{}
	for _, kv := range nodeStripHopByHop(open.Headers) {
		// gorilla sets Upgrade/Connection/Sec-WebSocket-* itself; skip those.
		switch kv.Key {
		case "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Extensions", "Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Host":
			continue
		}
		hdr.Add(kv.Key, kv.Value)
	}

	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	var conn *websocket.Conn
	var lastErr error
	for _, addr := range resolveContainerAddrs(serverUUID, int(open.TargetPort)) {
		c, _, err := dialer.Dial("ws://"+addr+open.Path, hdr)
		if err == nil {
			conn = c
			break
		}
		lastErr = err
	}
	if conn == nil {
		msg := "container ws unreachable"
		if lastErr != nil {
			msg = lastErr.Error()
		}
		cc.send(errorMsg(reqID, 502, msg))
		return
	}

	bridge := &wsBridge{conn: conn, inbound: make(chan *pb.NodeMessage, 64), done: make(chan struct{})}
	m.wsMu.Lock()
	m.wsBridges[reqID] = bridge
	m.wsMu.Unlock()

	// container -> Core pump (background goroutine)
	go func() {
		defer func() {
			cc.send(&pb.NodeMessage{RequestId: reqID, Payload: &pb.NodeMessage_WsClose{WsClose: &pb.WsClose{Code: 1000, Reason: ""}}})
			m.closeWSBridge(reqID)
		}()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			frags := splitWSFragments(data, wsFragmentSize)
			for i, f := range frags {
				if serr := cc.send(&pb.NodeMessage{
					RequestId: reqID,
					Payload:   &pb.NodeMessage_WsFrame{WsFrame: &pb.WsFrame{Opcode: int32(mt), Data: f, Fin: i == len(frags)-1}},
				}); serr != nil {
					return
				}
			}
		}
	}()

	// Core -> container pump (this goroutine): consume routed inbound messages.
	go func() {
		for {
			select {
			case <-bridge.done:
				return
			case msg := <-bridge.inbound:
				if cl := msg.GetWsClose(); cl != nil {
					m.closeWSBridge(reqID)
					return
				}
				fr := msg.GetWsFrame()
				if fr == nil {
					continue
				}
				bridge.rxBuf = append(bridge.rxBuf, fr.Data...)
				if fr.Opcode != 0 {
					bridge.rxOpcode = int(fr.Opcode)
				}
				if !fr.Fin {
					continue // wait for the last fragment
				}
				op := bridge.rxOpcode
				if op == 0 {
					op = websocket.TextMessage
				}
				payload := bridge.rxBuf
				bridge.rxBuf = nil
				bridge.rxOpcode = 0
				if err := conn.WriteMessage(op, payload); err != nil {
					m.closeWSBridge(reqID)
					return
				}
			}
		}
	}()
	log.Printf("gRPC Mesh: ws bridge opened (request_id=%s, server=%s)", reqID, serverUUID)
}

// routeWSInbound delivers a Core->node WsFrame/WsClose to the open bridge.
func (m *MeshManager) routeWSInbound(reqID string, msg *pb.NodeMessage) {
	m.wsMu.Lock()
	bridge, ok := m.wsBridges[reqID]
	m.wsMu.Unlock()
	if !ok {
		return
	}
	select {
	case bridge.inbound <- msg:
	case <-bridge.done:
	}
}

// closeWSBridge tears down and unregisters a bridge (idempotent).
func (m *MeshManager) closeWSBridge(reqID string) {
	m.wsMu.Lock()
	bridge, ok := m.wsBridges[reqID]
	if ok {
		delete(m.wsBridges, reqID)
	}
	m.wsMu.Unlock()
	if ok {
		bridge.close()
	}
}
