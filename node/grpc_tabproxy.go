package main

// Node-side handler for the custom-tab HTTP reverse proxy (WS5). Core sends
// HttpProxyReq; we dial the server's own container exactly like RCON does
// (mc_<uuid>:port on the dylaris_net overlay, and nothing else), perform the
// HTTP request, and stream the response head + body back to Core. The target
// is always the server's own container port - never an arbitrary host - so
// this introduces no SSRF pivot.

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	pb "dylaris-proto/node"
)

// proxyHTTPClient does NOT follow redirects (Core forwards 3xx to the browser
// as-is) and caps the round trip so an unresponsive container surfaces fast.
var proxyHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// hop-by-hop headers (RFC 7230 6.1) that must never be forwarded in either
// direction. Compared case-insensitively.
var nodeHopByHop = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
}

// containerAddr returns the ONE address a proxied tab may be dialled at: the
// server's own container on the dylaris_net overlay.
//
// There used to be a 127.0.0.1:<port> fallback here, described as "for
// single-host dev" and as mirroring grpc_rcon.go. It mirrored nothing: RCON
// deliberately has no such fallback, and says why - the node is containerized,
// so its own loopback is never the MC container. The difference that made it
// worse here is that the port comes from the tab config, i.e. from the tenant
// who owns the server. Whenever the container dial failed (any port their
// container does not listen on), the node fell through to probing its OWN
// loopback on a caller-chosen port, which is exactly the "never an arbitrary
// host, no SSRF pivot" property the file header claims.
func containerAddr(serverUUID string, port int) string {
	return fmt.Sprintf("mc_%s:%d", serverUUID, port)
}

// nodeStripHopByHop returns the subset of headers that are safe to forward.
func nodeStripHopByHop(hs []*pb.HttpHeader) []*pb.HttpHeader {
	out := make([]*pb.HttpHeader, 0, len(hs))
	for _, h := range hs {
		if nodeHopByHop[strings.ToLower(h.Key)] {
			continue
		}
		out = append(out, h)
	}
	return out
}

// headersToProto flattens an http.Header (multi-value) into the wire slice.
func headersToProto(h http.Header) []*pb.HttpHeader {
	out := make([]*pb.HttpHeader, 0, len(h))
	for k, vals := range h {
		for _, v := range vals {
			out = append(out, &pb.HttpHeader{Key: k, Value: v})
		}
	}
	return out
}

// handleHTTPProxy performs the container HTTP request and streams the response
// back: one HttpProxyRespHead, then DataChunk(s), then a final TransferDone.
func (h *StreamHandler) handleHTTPProxy(reqID, serverUUID string, req *pb.HttpProxyReq, send func(*pb.NodeMessage) error) {
	if req == nil || req.TargetPort < 1 || req.TargetPort > 65535 {
		send(errorMsg(reqID, 502, "invalid proxy target"))
		return
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	// One address, one attempt: there is no second candidate to fall through to
	// (see containerAddr), so a failure here is reported rather than retried
	// somewhere the target cannot be.
	url := "http://" + containerAddr(serverUUID, int(req.TargetPort)) + req.Path
	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}
	hreq, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		send(errorMsg(reqID, 502, err.Error()))
		return
	}
	// Forward only the sanitized headers Core sent. Core already dropped
	// the panel session cookie/Authorization; we drop hop-by-hop here too.
	for _, kv := range nodeStripHopByHop(req.Headers) {
		hreq.Header.Add(kv.Key, kv.Value)
	}
	resp, err := proxyHTTPClient.Do(hreq)
	if err != nil {
		send(errorMsg(reqID, 502, err.Error()))
		return
	}
	h.streamProxyResponse(reqID, resp, send)
}

// streamProxyResponse writes the response head then the body in 64KB chunks.
func (h *StreamHandler) streamProxyResponse(reqID string, resp *http.Response, send func(*pb.NodeMessage) error) {
	defer resp.Body.Close()
	if err := send(&pb.NodeMessage{
		RequestId: reqID,
		Payload: &pb.NodeMessage_HttpProxyRespHead{
			HttpProxyRespHead: &pb.HttpProxyRespHead{
				StatusCode: int32(resp.StatusCode),
				Headers:    nodeStripHopByHop(headersToProto(resp.Header)),
			},
		},
	}); err != nil {
		return
	}

	buf := make([]byte, chunkSize)
	var offset int64
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := send(&pb.NodeMessage{
				RequestId: reqID,
				Payload:   &pb.NodeMessage_Chunk{Chunk: &pb.DataChunk{Data: chunk, Offset: offset}},
			}); err != nil {
				return
			}
			offset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			// Head already sent; a mid-body error just ends the stream.
			break
		}
	}
	send(&pb.NodeMessage{
		RequestId: reqID,
		Payload:   &pb.NodeMessage_TransferDone{TransferDone: &pb.TransferDone{TotalBytes: offset}},
	})
}
