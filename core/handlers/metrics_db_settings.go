package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"dylaris-core/metrics"
	"dylaris-core/services"
)

// MetricsDBHandler is where the long-term statistics choose their database:
// the Core one, or a separate TimescaleDB.
//
// It lives beside the switch that turns recording on, because the two are one
// decision in practice - the question "should this platform keep history"
// arrives with "and where" attached, and answering the first without the second
// is what made this configurable only through an environment variable.
type MetricsDBHandler struct {
	state *AppState
}

func NewMetricsDBHandler(state *AppState) *MetricsDBHandler {
	return &MetricsDBHandler{state: state}
}

// metricsDBRequest is one screen's worth of decision: whether to record, and
// where. They arrive together because they ARE together - see the note on Set.
type metricsDBRequest struct {
	services.MetricsDBTarget
	Enabled bool `json:"enabled"`
	// NoPassword says the database has none, as opposed to the field simply
	// being left alone. Without it a blank field means only "keep what is
	// stored", so a password once saved could never be taken back off - and
	// "no password" is the CORRECT configuration for a database reached over a
	// private network, which is how the reference deployment runs it.
	NoPassword bool `json:"noPassword"`
}

// metricsDBResponse is what the form renders from. The password is never in it.
type metricsDBResponse struct {
	services.MetricsDBTarget
	// Enabled is the long-term statistics switch (`feature_metrics_enabled`),
	// owned by this endpoint rather than the feature bundle.
	Enabled bool `json:"enabled"`
	// PasswordSet lets the form show that one is stored without carrying it.
	// Without this the field looks empty and identical to "there is none",
	// which are opposite configurations here.
	PasswordSet bool `json:"passwordSet"`
	// Active describes what is being written RIGHT NOW, which is not always
	// what is configured: an unreachable target leaves the previous one running.
	Active metricsDBActive `json:"active"`
	// CoreTimescale reports whether the CORE database has the extension. It
	// does not change the resolution there - see the note in Test - but it
	// decides whether the table is chunked and compressed.
	CoreTimescale bool `json:"coreTimescale"`
}

type metricsDBActive struct {
	// Recording is false when nothing is open: the feature is off, or the
	// database could not be reached at boot.
	Recording bool `json:"recording"`
	// Separate is true when a database of its own is in use.
	Separate bool `json:"separate"`
	// Resolution is "minute" or "hour", empty when nothing is recording.
	Resolution string `json:"resolution,omitempty"`
}

// Get GET /api/admin/settings/metrics-db. PANEL settings.read.
func (h *MetricsDBHandler) Get(w http.ResponseWriter, r *http.Request) {
	stored := services.LoadMetricsDBTarget(h.state.Store)
	pwSet := stored.Password != ""
	stored.Password = ""

	tsInstalled, _ := h.state.Store.TimescaleEnabled(r.Context())

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"settings": metricsDBResponse{
			MetricsDBTarget: stored,
			Enabled:         h.state.FeatureFlags.Get(r.Context(), services.MetricsEnabledSetting, false),
			PasswordSet:     pwSet,
			Active:          h.activeState(),
			CoreTimescale:   tsInstalled,
		},
	})
}

func (h *MetricsDBHandler) activeState() metricsDBActive {
	handle := h.state.Metrics.Handle()
	if handle == nil {
		return metricsDBActive{}
	}
	res := "hour"
	if handle.Resolution == metrics.ResolutionDedicated {
		res = "minute"
	}
	return metricsDBActive{
		Recording:  true,
		Separate:   handle.Dedicated != nil,
		Resolution: res,
	}
}

// Test POST /api/admin/settings/metrics-db/test - probe without saving.
// PANEL settings.write (it reaches out to a host the caller names).
func (h *MetricsDBHandler) Test(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndMerge(w, r)
	if !ok {
		return
	}
	// The switch is irrelevant here: a test answers "would this work", which is
	// the same question whether or not recording is currently on.
	target := req.MetricsDBTarget

	// The Core database needs no probe: Core is talking to it right now, so
	// "can it be reached" is answered by this request existing. What is worth
	// reporting is what the operator will GET.
	if !target.IsSeparate() {
		ts, _ := h.state.Store.TimescaleEnabled(r.Context())
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":   true,
			"ok":        true,
			"severity":  "ok",
			"timescale": ts,
			"message":   coreDBOutcome(ts),
		})
		return
	}

	if err := target.Validate(); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	probe := services.ProbeMetricsDB(r.Context(), target)
	if !probe.Reachable {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"ok":       false,
			"severity": "error",
			"message":  "Could not connect: " + probe.Error,
		})
		return
	}

	sev, msg := separateDBOutcome(probe)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":   true,
		"ok":        true,
		"severity":  sev,
		"timescale": probe.Timescale,
		"version":   probe.Version,
		"message":   msg,
	})
}

// coreDBOutcome says what the Core database gives you.
//
// Hour buckets EITHER WAY, and that is worth being exact about rather than
// tidy: the resolution follows from which database is used, not from what is
// installed in it (core/metrics.Open). The extension only decides whether the
// table is chunked and compressed, so a reader who is told "TimescaleDB found"
// must not conclude they are now getting minutes.
func coreDBOutcome(timescale bool) string {
	if timescale {
		return "Connected. Hour buckets in the Core database, stored as a compressed hypertable. " +
			"Minute resolution needs a separate database - it is not something this extension turns on."
	}
	return "Connected. Hour buckets in the Core database, as a plain table - a few hundred megabytes a year, " +
		"and no extension required. Choose a separate TimescaleDB for minute resolution."
}

// separateDBOutcome judges a reachable separate database.
//
// Missing TimescaleDB is a WARNING and not a refusal, and both halves of that
// are deliberate. It is not an error because the data is identical and the
// platform works; it is not silent because minute buckets on a plain table is
// the one combination here that ends badly - roughly a hundred million rows a
// year with nothing to chunk or compress them, on a screen nobody looks at
// until it is slow.
func separateDBOutcome(p services.MetricsDBProbe) (severity, message string) {
	v := ""
	if p.Version != "" {
		v = " (PostgreSQL " + p.Version + ")"
	}
	if p.Timescale {
		return "ok", "Connected" + v + ". TimescaleDB found: minute buckets, chunked and compressed after seven days."
	}
	return "warning", "Connected" + v + ", but the TimescaleDB extension is not installed in this database. " +
		"Minute buckets would go into a plain table - on the order of a hundred million rows a year, with no " +
		"chunking or compression. Use a TimescaleDB image, or keep the Core database at hour resolution."
}

// Set PUT /api/admin/settings/metrics-db. PANEL settings.write.
func (h *MetricsDBHandler) Set(w http.ResponseWriter, r *http.Request) {
	req, ok := h.decodeAndMerge(w, r)
	if !ok {
		return
	}
	target := req.MetricsDBTarget
	if err := target.Validate(); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// A separate database must answer before it is stored. Saving an
	// unreachable one would leave the panel showing a target that is not being
	// written to, with the recording quietly still going somewhere else - the
	// single most confusing state this screen can be in.
	//
	// The Core database is exempt: this request proves it is reachable.
	var probe services.MetricsDBProbe
	if target.IsSeparate() {
		probe = services.ProbeMetricsDB(r.Context(), target)
		if !probe.Reachable {
			sendJSONError(w, "Could not connect to that database, so it was not saved: "+probe.Error,
				http.StatusBadGateway)
			return
		}
	}

	if err := services.SaveMetricsDBTarget(h.state.Store, target); err != nil {
		sendJSONError(w, "Save failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// The target is written BEFORE the switch, and that order is the point.
	// Recording begins the instant the flag is true, and the first bucket it
	// writes is at whatever resolution the stored target implies - so a switch
	// that landed first would open a window recording into the OLD target, and
	// the history would start at a resolution nobody chose.
	if err := h.setRecording(r, req.Enabled); err != nil {
		sendJSONError(w, "The database was saved but the switch was not: "+err.Error(),
			http.StatusInternalServerError)
		return
	}

	// Apply it live. A failure here is reported but does NOT unsave: the stored
	// target is what the next boot uses, and telling the operator "saved, not
	// yet in use" beats a form that refuses a correct setting because of a
	// transient error at the moment of applying it.
	applyErr := ""
	if err := h.state.Metrics.Apply(target.DSN()); err != nil {
		applyErr = err.Error()
	}

	stored := target
	stored.Password = ""
	resp := map[string]interface{}{
		"success": true,
		"settings": metricsDBResponse{
			MetricsDBTarget: stored,
			Enabled:         req.Enabled,
			PasswordSet:     target.Password != "",
			Active:          h.activeState(),
		},
	}
	if applyErr != "" {
		resp["warning"] = "Saved, but switching to it now failed (" + applyErr +
			"). It will be used after the next Core restart."
	} else if target.IsSeparate() && !probe.Timescale {
		_, resp["warning"] = separateDBOutcome(probe)
	}
	json.NewEncoder(w).Encode(resp)
}

// decodeAndMerge reads the form and restores the stored password when the field
// was left blank AND the connection identity is unchanged.
//
// The identity condition is the part that matters. Carrying a blank password
// forward unconditionally would let a changed host silently receive the old
// database's credential - the same reasoning as the Core storage form, where it
// is written out at length.
// setRecording writes the long-term statistics switch.
//
// Deliberately the same three steps the feature bundle performs - persist,
// invalidate the cached flag, publish features.changed - because the panel gates
// the Statistics screen on that event and nothing here would tell it otherwise.
// The KEY is unchanged (`feature_metrics_enabled`); only the endpoint that owns
// writing it moved.
func (h *MetricsDBHandler) setRecording(r *http.Request, on bool) error {
	if err := h.state.Store.SetSetting(services.MetricsEnabledSetting, boolStr(on)); err != nil {
		return err
	}
	h.state.FeatureFlags.Invalidate(services.MetricsEnabledSetting)
	h.state.Events.Publish(r.Context(), "features.changed", map[string]interface{}{
		"feature": "metrics",
		"enabled": on,
	})
	return nil
}

func (h *MetricsDBHandler) decodeAndMerge(w http.ResponseWriter, r *http.Request) (metricsDBRequest, bool) {
	var req metricsDBRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return metricsDBRequest{}, false
	}
	req.MetricsDBTarget = req.MetricsDBTarget.Normalize()
	switch {
	case req.NoPassword:
		// Explicit, so it CLEARS. This is the only way a stored password can be
		// removed; every other path here either sets one or keeps one.
		req.Password = ""
	case req.Password == "":
		existing := services.LoadMetricsDBTarget(h.state.Store)
		if sameMetricsEndpoint(req.MetricsDBTarget, existing) {
			req.Password = existing.Password
		}
	}
	return req, true
}

func sameMetricsEndpoint(a, b services.MetricsDBTarget) bool {
	return strings.EqualFold(a.Host, b.Host) &&
		a.Port == b.Port &&
		a.DBName == b.DBName &&
		a.User == b.User
}
