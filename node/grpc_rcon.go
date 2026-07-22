package main

// Node-side handler for the RCON-exec RPC. Core sends us the
// server UUID + command + password + (optionally) the RCON port; we open a
// TCP connection to the container's RCON socket and forward the reply.
//
// Address resolution: the MC container is a sibling on the dylaris_net
// overlay, reachable by its container name mc_<uuid>. Docker DNS resolves it
// from inside the node's network namespace, so we dial mc_<uuid>:<rcon_port>
// and nothing else. There is no 127.0.0.1 fallback: the node is containerized
// and its own loopback is never the MC container, so a fallback dial there
// could only fail with a misleading "connection refused" that masks the real
// overlay error (e.g. RCON not listening because enable-rcon was never
// written to server.properties). We surface the mc_<uuid> error directly.

import (
	"fmt"
	"time"

	pb "dylaris-proto/node"
)

func (h *StreamHandler) handleRconExec(requestID, serverUUID string, req *pb.RconExecReq) *pb.NodeMessage {
	start := time.Now()
	if req == nil || req.Command == "" {
		return rconErrMsg(requestID, "empty command", start)
	}
	if req.RconPassword == "" {
		return rconErrMsg(requestID, "rcon password not configured for this server", start)
	}

	port := int(req.RconPort)
	if port == 0 {
		port = rconDefaultPortVar
	}
	timeout := time.Duration(req.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = rconDefaultTimeout
	}

	addr := fmt.Sprintf("mc_%s:%d", serverUUID, port)
	out, err := execRcon(addr, req.RconPassword, req.Command, timeout)
	if err != nil {
		return rconErrMsg(requestID, err.Error(), start)
	}
	return &pb.NodeMessage{
		RequestId:  requestID,
		ServerUuid: serverUUID,
		Payload: &pb.NodeMessage_RconExecResp{
			RconExecResp: &pb.RconExecResp{
				Ok:         true,
				Output:     out,
				DurationMs: time.Since(start).Milliseconds(),
			},
		},
	}
}

func rconErrMsg(requestID, msg string, start time.Time) *pb.NodeMessage {
	return &pb.NodeMessage{
		RequestId: requestID,
		Payload: &pb.NodeMessage_RconExecResp{
			RconExecResp: &pb.RconExecResp{
				Ok:         false,
				Error:      msg,
				DurationMs: time.Since(start).Milliseconds(),
			},
		},
	}
}
