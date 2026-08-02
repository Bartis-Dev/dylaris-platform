package main

import (
	"net"
	"net/http"
	"time"
)

// HTTP clients for the installers.
//
// These calls reach third parties DYLARIS does not control - Mojang, PaperMC,
// the NeoForge Maven. `http.Get` uses http.DefaultClient, which has NO timeout
// at all, so a server that accepts the connection and then goes quiet hangs the
// installer forever. The server stays in `installing` and never resolves either
// way, which is worse than a failed install: a failure can be retried.
//
// Two clients, because the two shapes need opposite things.

// installerMetaTimeout bounds a metadata fetch end to end. These responses are
// small JSON or XML documents; anything near a minute means the far side is
// broken, not slow.
const installerMetaTimeout = 30 * time.Second

// installerMetaClient fetches version manifests and build metadata.
var installerMetaClient = &http.Client{Timeout: installerMetaTimeout}

// installerDownloadClient fetches server JARs, which are large and legitimately
// slow on a thin link, so it deliberately has NO overall deadline - a blanket
// timeout would abort a working download on a slow connection.
//
// Instead the phases that must not hang are bounded individually: connecting,
// the TLS handshake, and waiting for response headers. A stall in the middle of
// the BODY is still unbounded - catching that needs a per-read deadline around
// io.Copy, not a Transport setting - so this narrows the window rather than
// closing it.
//
// Proxy and ForceAttemptHTTP2 are restated because a custom Transport does NOT
// inherit http.DefaultTransport's defaults: leaving Proxy nil silently ignores
// HTTP_PROXY/HTTPS_PROXY, which would break every download on a node behind a
// corporate proxy that worked before this file existed.
var installerDownloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
	},
}
