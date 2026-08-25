package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The DNS credential is entered in the panel and STORED IN THE GATEWAY HUB.
//
// Core keeps nothing. It forwards the form and reports what came back. That is
// deliberate and not laziness: the Hub's reconciler creates missing records and
// DELETES stale ones, and its "an edge name wins over a relay name" guard only
// holds inside one plan. Two components each holding a copy of the credential is
// one short step from two components each writing records, which shows up as an
// address that flaps and reads like a provider fault for weeks.
//
// So the row lives in exactly one database, in the Hub, and this file is a pipe.

// dnsForwardTimeout bounds the hop. The Hub only touches its own Postgres here -
// no provider call - so a slow answer means the Hub is unwell, and a panel that
// hangs on it is worse than one that says so.
const dnsForwardTimeout = 8 * time.Second

// hubProofPrincipal is the name Core signs its requests to the Hub under.
//
// It is part of the signed message, not an authorization: anything holding
// CLUSTER_SECRET can sign any principal. The real bar for this endpoint is
// possession of that secret, which is why the read direction never returns the
// token - overwriting is recoverable by retyping, exfiltration is not.
const hubProofPrincipal = "core"

// GatewayDNSHandler forwards the panel's DNS settings to the gateway Hub.
type GatewayDNSHandler struct {
	state *AppState
}

func NewGatewayDNSHandler(state *AppState) *GatewayDNSHandler {
	return &GatewayDNSHandler{state: state}
}

// dnsConfigPayload is the Hub's answer. Note the absence of a token field: the
// Hub does not send one, and this struct not having one is what guarantees it
// could not be forwarded on by accident.
type dnsConfigPayload struct {
	Provider  string   `json:"provider"`
	Zones     []string `json:"zones"`
	Enabled   bool     `json:"enabled"`
	HasToken  bool     `json:"has_token"`
	EnvLocked bool     `json:"env_locked"`
	Providers []struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	} `json:"providers"`
}

// Get GET /api/settings/gateway/dns - PANEL settings.read.
func (h *GatewayDNSHandler) Get(w http.ResponseWriter, r *http.Request) {
	h.forward(w, r, map[string]any{"action": "read"})
}

// Save PUT /api/settings/gateway/dns - PANEL settings.write.
//
// A blank token means "keep the stored one", decided by the Hub rather than
// here: the field is write-only in the UI, so an admin editing a zone must not
// silently erase the credential. Keeping that rule in one place means this
// forwarder cannot drift from it.
func (h *GatewayDNSHandler) Save(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var req struct {
		Provider string   `json:"provider"`
		Token    string   `json:"token"`
		Zones    []string `json:"zones"`
		Enabled  bool     `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		dnsError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	h.forward(w, r, map[string]any{
		"action":   "write",
		"provider": strings.TrimSpace(req.Provider),
		"token":    req.Token,
		"zones":    req.Zones,
		"enabled":  req.Enabled,
	})
}

func (h *GatewayDNSHandler) forward(w http.ResponseWriter, r *http.Request, body map[string]any) {
	hubURL := strings.TrimSpace(h.state.GatewayHubURL)
	if hubURL == "" {
		// Not an error. A platform-only install has no gateway and no records to
		// write, and the screen renders that state instead of a form that cannot
		// work.
		dnsJSON(w, http.StatusOK, map[string]any{
			"success":   true,
			"available": false,
		})
		return
	}
	if h.state.ClusterSecret == "" {
		dnsError(w, http.StatusServiceUnavailable, "CLUSTER_SECRET is not configured")
		return
	}

	ts := time.Now().Unix()
	body["principal"] = hubProofPrincipal
	body["ts"] = ts
	body["proof"] = hubProof(h.state.ClusterSecret, hubProofPrincipal, ts)

	payload, err := json.Marshal(body)
	if err != nil {
		dnsError(w, http.StatusInternalServerError, "could not build the request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), dnsForwardTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(hubURL, "/")+"/internal/dns-config", bytes.NewReader(payload))
	if err != nil {
		dnsError(w, http.StatusInternalServerError, "could not build the request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		dnsError(w, http.StatusBadGateway, "the gateway hub did not answer")
		return
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		dnsError(w, http.StatusBadGateway, "the gateway hub's answer could not be read")
		return
	}

	if resp.StatusCode != http.StatusOK {
		// The Hub's rejections are written for the person at the form ("name at
		// least one zone"), so they are passed through rather than flattened.
		// Its 401 is not: that is a cluster-secret mismatch between the two
		// services, which is an operator problem and needs to say so.
		msg := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusUnauthorized {
			msg = "the gateway hub rejected this platform's CLUSTER_SECRET - the two must match"
		}
		if msg == "" {
			msg = fmt.Sprintf("the gateway hub returned %d", resp.StatusCode)
		}
		dnsError(w, http.StatusBadRequest, msg)
		return
	}

	var cfg dnsConfigPayload
	if err := json.Unmarshal(raw, &cfg); err != nil {
		dnsError(w, http.StatusBadGateway, "the gateway hub's answer could not be read")
		return
	}
	dnsJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"available": true,
		"config":    cfg,
	})
}

// hubProof reproduces the gateway's proof-of-possession:
// hex(HMAC-SHA256(CLUSTER_SECRET, principal + ":" + ts)).
//
// FROZEN CROSS-REPO CONTRACT. The other side is
// `gateway/pkg/redisacl.Proof` / `VerifyProof`. It is duplicated rather than
// shared because the two repositories build independently, exactly like the beam
// stream header - and like that header it must not change on one side alone. The
// secret itself never travels.
func hubProof(clusterSecret, principal string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(clusterSecret))
	mac.Write([]byte(principal + ":" + strconv.FormatInt(ts, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func dnsError(w http.ResponseWriter, code int, msg string) {
	dnsJSON(w, code, map[string]any{"success": false, "message": msg})
}

func dnsJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
