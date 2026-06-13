package main

// Node-side handler for the RCON-exec RPC. Core sends us the
// server UUID + command + password + (optionally) the RCON port; we open a
// TCP connection to the container's RCON socket and forward the reply.
//
// Address resolution V1:
//   - mc_<uuid> on the dylaris_net overlay if reachable (Docker DNS will
//     resolve it from inside the node's network namespace)
//   - 127.0.0.1:<rcon_port> as a fallback for host-network setups / dev
//   We try overlay first, fall back on dial failure. Real prod is overlay-
//   only; the fallback covers single-host dev where the container publishes
//   its RCON port on the host.

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

	addrs := []string{
		fmt.Sprintf("mc_%s:%d", serverUUID, port),
		fmt.Sprintf("127.0.0.1:%d", port),
	}

	var lastErr error
	for _, addr := range addrs {
		out, err := execRcon(addr, req.RconPassword, req.Command, timeout)
		if err == nil {
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
		lastErr = err
	}
	return rconErrMsg(requestID, lastErr.Error(), start)
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
