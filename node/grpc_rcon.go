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
//
// "and nothing else" was not true, which is what resolveContainerIP below is
// for. When the container is absent from the network - it is stopped, or the
// name is simply wrong - Docker's embedded resolver does not stop there: it
// forwards the name upstream, and a wildcard DNS record anywhere in that chain
// answers with a PUBLIC address. Observed live on a stopped server:
//
//	dial mc_<uuid>:25575: dial tcp 46.225.53.182:25575: i/o timeout
//
// A timeout is the lucky outcome. Had anything been listening on that port,
// execRcon's first act is to write SERVERDATA_AUTH carrying the server's RCON
// password in the clear, to a host chosen by whoever controls that DNS answer.

import (
	"fmt"
	"net"
	"time"

	pb "dylaris-proto/node"
)

// resolveContainerIP resolves a sibling container's name and returns an address
// that can plausibly BE that container.
//
// The MC container always sits on a Docker bridge/overlay network, so its address
// is always private. A public answer therefore means the name did not resolve
// locally at all and something upstream replied instead - never our container,
// and never something to hand a password to.
func resolveContainerIP(name string, port int) (string, error) {
	ips, err := net.LookupIP(name)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return net.JoinHostPort(ip.String(), fmt.Sprint(port)), nil
		}
	}
	// Naming the addresses is deliberate: this is the line that tells an operator
	// their internal name is being answered by a public resolver.
	return "", fmt.Errorf("%s resolved only to non-private address(es) %v — the container is not on this node's network "+
		"(is the server running?) and the name is being answered from outside; refusing to send RCON credentials there",
		name, ips)
}

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

	addr, err := resolveContainerIP(fmt.Sprintf("mc_%s", serverUUID), port)
	if err != nil {
		return rconErrMsg(requestID, err.Error(), start)
	}
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
