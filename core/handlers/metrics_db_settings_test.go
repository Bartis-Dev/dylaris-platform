package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"dylaris-core/services"
	"dylaris-core/store"
)

// metricsDBStore is the settings table plus the one extension probe this
// screen asks for.
type metricsDBStore struct {
	store.Store
	vals     map[string]string
	timescal bool
}

func (s *metricsDBStore) GetSetting(k string) (string, error) { return s.vals[k], nil }
func (s *metricsDBStore) SetSetting(k, v string) error        { s.vals[k] = v; return nil }
func (s *metricsDBStore) TimescaleEnabled(context.Context) (bool, error) {
	return s.timescal, nil
}

func metricsDBHandlerFor(st *metricsDBStore) *MetricsDBHandler {
	// FeatureFlags reads the same store: this endpoint owns the recording
	// switch now, so the two cannot be given different views of it.
	return NewMetricsDBHandler(&AppState{
		Store:        st,
		FeatureFlags: services.NewFeatureFlags(st),
	})
}

func storedSeparate() map[string]string {
	return map[string]string{
		services.MetricsDBModeSetting:     services.MetricsDBModeSeparate,
		services.MetricsDBHostSetting:     "metricsdb",
		services.MetricsDBPortSetting:     "5432",
		services.MetricsDBNameSetting:     "dylaris_metrics",
		services.MetricsDBUserSetting:     "metrics",
		services.MetricsDBPasswordSetting: "stored-secret",
		services.MetricsDBSSLModeSetting:  "disable",
	}
}

// The password is write-only, exactly like the S3 secret on the storage form.
// A GET that returned it would put a live database credential into every
// browser tab, every proxy log and every screenshot of this page.
func TestTheStoredPasswordIsNeverSentToTheBrowser(t *testing.T) {
	h := metricsDBHandlerFor(&metricsDBStore{vals: storedSeparate()})
	w := httptest.NewRecorder()
	h.Get(w, httptest.NewRequest("GET", "/api/admin/settings/metrics-db", nil))

	body := w.Body.String()
	if strings.Contains(body, "stored-secret") {
		t.Fatalf("the password was emitted in the GET body: %s", body)
	}

	var resp struct {
		Settings struct {
			PasswordSet bool   `json:"passwordSet"`
			Password    string `json:"password"`
			Host        string `json:"host"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Settings.Password != "" {
		t.Errorf("password field = %q; it must never be populated", resp.Settings.Password)
	}
	// "there is one, you just cannot see it" and "there is none" are opposite
	// configurations, and the form cannot tell them apart without this flag.
	if !resp.Settings.PasswordSet {
		t.Error("passwordSet is false although one is stored, so the form cannot show that one exists")
	}
	if resp.Settings.Host != "metricsdb" {
		t.Errorf("host = %q; the rest of the target must still be readable", resp.Settings.Host)
	}
}

// A blank password means "keep the one you have" - but only while the form
// still points at the SAME database. The reasoning is the Core-storage form's,
// written out at length there: carrying it across a host change would hand one
// database's credential to whatever host an admin (or an attacker with this
// form) typed next.
func TestABlankPasswordIsKeptOnlyForTheSameEndpoint(t *testing.T) {
	same := `{"mode":"separate","host":"metricsdb","port":"5432","dbName":"dylaris_metrics","user":"metrics","password":""}`
	moved := `{"mode":"separate","host":"attacker.example","port":"5432","dbName":"dylaris_metrics","user":"metrics","password":""}`

	for _, c := range []struct {
		name string
		body string
		want string
	}{
		{"unchanged endpoint keeps the stored password", same, "stored-secret"},
		{"a different host gets nothing", moved, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := metricsDBHandlerFor(&metricsDBStore{vals: storedSeparate()})
			got, ok := h.decodeAndMerge(httptest.NewRecorder(), httptest.NewRequest("PUT", "/x", strings.NewReader(c.body)))
			if !ok {
				t.Fatal("the body was rejected")
			}
			if got.Password != c.want {
				t.Fatalf("password = %q, want %q", got.Password, c.want)
			}
		})
	}
}

// The Core database records HOUR buckets whether or not TimescaleDB is
// installed in it - the resolution follows from WHICH database is used, not
// what is in it (core/metrics.Open). A test button that answered "TimescaleDB
// found" without saying that would leave an operator expecting minutes.
func TestTheCoreDatabaseNeverPromisesMinuteResolution(t *testing.T) {
	for _, ts := range []bool{true, false} {
		msg := strings.ToLower(coreDBOutcome(ts))
		if !strings.Contains(msg, "hour") {
			t.Errorf("timescale=%v: %q does not say hour buckets", ts, msg)
		}
		// It may TELL you minute resolution needs a separate database; it must
		// not claim you are getting it here.
		if strings.Contains(msg, "minute buckets") {
			t.Errorf("timescale=%v: %q promises minute buckets from the Core database", ts, msg)
		}
	}
	if !strings.Contains(coreDBOutcome(true), "hypertable") {
		t.Error("with the extension present the answer should still say what it changes")
	}
}

// A separate database without the extension is allowed and LOUD. Refusing it
// would block a working setup; saying nothing would leave minute buckets
// accumulating in a plain table, which is the one combination here that ends
// badly and does so silently.
func TestASeparateDatabaseWithoutTimescaleWarnsRatherThanFails(t *testing.T) {
	sev, msg := separateDBOutcome(services.MetricsDBProbe{Reachable: true, Version: "16.15"})
	if sev != "warning" {
		t.Fatalf("severity = %q, want warning", sev)
	}
	for _, want := range []string{"TimescaleDB", "plain table"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the warning does not mention %q: %s", want, msg)
		}
	}

	sev, msg = separateDBOutcome(services.MetricsDBProbe{Reachable: true, Timescale: true, Version: "16.15"})
	if sev != "ok" {
		t.Fatalf("severity with the extension present = %q, want ok", sev)
	}
	if !strings.Contains(msg, "minute") {
		t.Errorf("the success message does not say what you get: %s", msg)
	}
}

// Testing the Core database must not require a probe: this request is itself
// proof that Core can reach it, and dialling it again would only add a way for
// a working setup to report a failure.
func TestTestingTheCoreDatabaseAnswersWithoutDialling(t *testing.T) {
	h := metricsDBHandlerFor(&metricsDBStore{vals: map[string]string{}, timescal: true})
	w := httptest.NewRecorder()
	h.Test(w, httptest.NewRequest("POST", "/x", strings.NewReader(`{"mode":"core"}`)))

	var resp struct {
		OK        bool   `json:"ok"`
		Severity  string `json:"severity"`
		Timescale bool   `json:"timescale"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Severity != "ok" {
		t.Fatalf("core test returned ok=%v severity=%q", resp.OK, resp.Severity)
	}
	if !resp.Timescale {
		t.Error("the extension is installed in this store but the answer says otherwise")
	}
}

// A form missing its host is refused before anything is dialled, and the
// message names the field so the panel can point at it.
func TestAnIncompleteSeparateTargetIsRefusedBeforeDialling(t *testing.T) {
	h := metricsDBHandlerFor(&metricsDBStore{vals: map[string]string{}})
	w := httptest.NewRecorder()
	h.Test(w, httptest.NewRequest("POST", "/x", strings.NewReader(`{"mode":"separate","dbName":"d","user":"u"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "host") {
		t.Errorf("the error does not name the missing field: %s", w.Body.String())
	}
}

// Nothing open is a normal state (the feature is off by default), and the
// screen has to render it rather than panic on a nil handle.
func TestTheActiveBlockCopesWithNothingRecording(t *testing.T) {
	h := metricsDBHandlerFor(&metricsDBStore{vals: map[string]string{}})
	got := h.activeState()
	if got.Recording || got.Resolution != "" {
		t.Fatalf("activeState with no manager = %+v; want the empty state", got)
	}
}

// The switch and the database are saved together, by one request, because they
// are one decision: recording starts at the moment the flag goes true and the
// first bucket lands at whatever resolution the stored target implies. Two
// endpoints meant a window where those disagreed.
func TestOneSaveWritesBothTheSwitchAndTheTarget(t *testing.T) {
	st := &metricsDBStore{vals: map[string]string{}}
	h := metricsDBHandlerFor(st)

	w := httptest.NewRecorder()
	h.Set(w, httptest.NewRequest("PUT", "/x", strings.NewReader(`{"enabled":true,"mode":"core"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if st.vals[services.MetricsEnabledSetting] != "true" {
		t.Fatalf("the recording switch was not written: %q", st.vals[services.MetricsEnabledSetting])
	}
	if st.vals[services.MetricsDBModeSetting] != services.MetricsDBModeCore {
		t.Fatalf("the target was not written: %q", st.vals[services.MetricsDBModeSetting])
	}

	// And back off again, so the switch is genuinely written rather than only
	// ever set - a handler that wrote "true" unconditionally would pass above.
	w = httptest.NewRecorder()
	h.Set(w, httptest.NewRequest("PUT", "/x", strings.NewReader(`{"enabled":false,"mode":"core"}`)))
	if st.vals[services.MetricsEnabledSetting] != "false" {
		t.Fatalf("switching recording off did not write: %q", st.vals[services.MetricsEnabledSetting])
	}
}

// The GET has to carry the switch, or the merged card cannot render its own
// state and would show recording as off on every load.
func TestTheGetReportsWhetherRecordingIsOn(t *testing.T) {
	st := &metricsDBStore{vals: map[string]string{services.MetricsEnabledSetting: "true"}}
	w := httptest.NewRecorder()
	metricsDBHandlerFor(st).Get(w, httptest.NewRequest("GET", "/x", nil))

	var resp struct {
		Settings struct {
			Enabled bool `json:"enabled"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Settings.Enabled {
		t.Fatal("recording is on in the store but the GET says off")
	}
}

// A refused target must not switch recording on. Otherwise the one request
// half-succeeds into the worst state available: recording is on, and it is
// recording into whatever the PREVIOUS target was, at a resolution nobody chose
// on a screen that says something else.
func TestARefusedTargetLeavesTheSwitchAlone(t *testing.T) {
	st := &metricsDBStore{vals: map[string]string{services.MetricsEnabledSetting: "false"}}
	h := metricsDBHandlerFor(st)

	w := httptest.NewRecorder()
	h.Set(w, httptest.NewRequest("PUT", "/x", strings.NewReader(
		`{"enabled":true,"mode":"separate","host":"127.0.0.1","port":"1","dbName":"d","user":"u"}`)))

	if w.Code == http.StatusOK {
		t.Fatalf("an unreachable target was accepted: %s", w.Body.String())
	}
	if st.vals[services.MetricsEnabledSetting] != "false" {
		t.Fatalf("the switch moved to %q despite the target being refused",
			st.vals[services.MetricsEnabledSetting])
	}
	if st.vals[services.MetricsDBHostSetting] != "" {
		t.Fatalf("the refused target was stored anyway: %q", st.vals[services.MetricsDBHostSetting])
	}
}

// One setting, one writer.
//
// The recording switch lived in the feature bundle while its database lived
// here, so two endpoints wrote `feature_metrics_enabled` and the last save won:
// the feature card would have reverted a change made on the statistics card
// from its own stale copy of the bundle.
//
// Checked at the source rather than by driving Set, and the site is named
// exactly - the writes table in that file is the only way the bundle could
// touch this setting, so its absence there IS the invariant. Driving the
// handler would have proven the same thing through the modpack module sync,
// which is a different subsystem and would need a fake of its own here.
func TestTheFeatureBundleNoLongerWritesTheMetricsSwitch(t *testing.T) {
	src, err := os.ReadFile("feature_settings.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "MetricsEnabledSetting") {
		t.Fatal("the feature bundle references the statistics switch again; " +
			"two writers of one setting means the last save wins and the other card reverts it")
	}
	// And the payload field is gone, so an old body cannot smuggle a false in
	// through Go's zero value for an absent bool.
	if strings.Contains(string(src), "Metrics bool") {
		t.Fatal("featureSettingsPayload still has a Metrics field")
	}
}

// A stored password has to be removable, and a blank field cannot say that: it
// already means "keep the one you have". Without an explicit signal a password
// saved once could never be taken back off - and none is the CORRECT setting
// for a database reached over a private network, which is how this deployment
// runs its own.
func TestNoPasswordClearsAStoredOne(t *testing.T) {
	st := &metricsDBStore{vals: storedSeparate()}
	h := metricsDBHandlerFor(st)

	// Same endpoint, blank field, noPassword set: the stored one must go.
	got, ok := h.decodeAndMerge(httptest.NewRecorder(), httptest.NewRequest("PUT", "/x", strings.NewReader(
		`{"mode":"separate","host":"metricsdb","port":"5432","dbName":"dylaris_metrics","user":"metrics","password":"","noPassword":true}`)))
	if !ok {
		t.Fatal("the body was rejected")
	}
	if got.Password != "" {
		t.Fatalf("password = %q; ticking \"no password\" must clear the stored one", got.Password)
	}
}

// And the keep-what-is-stored behaviour has to survive that, or every save
// without a retyped password would silently wipe it.
func TestABlankFieldStillKeepsTheStoredPassword(t *testing.T) {
	h := metricsDBHandlerFor(&metricsDBStore{vals: storedSeparate()})
	got, _ := h.decodeAndMerge(httptest.NewRecorder(), httptest.NewRequest("PUT", "/x", strings.NewReader(
		`{"mode":"separate","host":"metricsdb","port":"5432","dbName":"dylaris_metrics","user":"metrics","password":""}`)))
	if got.Password != "stored-secret" {
		t.Fatalf("password = %q; a blank field without the flag must keep the stored one", got.Password)
	}
}

// The flag WINS over a value sent alongside it. From the panel the two can
// never disagree - ticking the box empties and disables the field - so a
// request carrying both is a client that is confused about its own state.
//
// Resolving that towards "no password" is the safer of the two directions: the
// connection then fails loudly if one was actually required, whereas the other
// way round would quietly authenticate with a credential the operator believes
// is switched off.
func TestTheNoPasswordFlagWinsOverAValueSentWithIt(t *testing.T) {
	h := metricsDBHandlerFor(&metricsDBStore{vals: storedSeparate()})
	got, _ := h.decodeAndMerge(httptest.NewRecorder(), httptest.NewRequest("PUT", "/x", strings.NewReader(
		`{"mode":"separate","host":"metricsdb","port":"5432","dbName":"dylaris_metrics","user":"metrics","password":"typed","noPassword":true}`)))
	if got.Password != "" {
		t.Fatalf("password = %q; the explicit flag must win over a value sent with it", got.Password)
	}
}

// Saving with the flag set actually writes the empty password through, so a
// reload shows passwordSet false rather than the old one coming back.
func TestClearingThePasswordSurvivesTheSave(t *testing.T) {
	st := &metricsDBStore{vals: storedSeparate()}
	h := metricsDBHandlerFor(st)

	w := httptest.NewRecorder()
	h.Set(w, httptest.NewRequest("PUT", "/x", strings.NewReader(
		`{"enabled":false,"mode":"core","noPassword":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if st.vals[services.MetricsDBPasswordSetting] != "" {
		t.Fatalf("the stored password survived the save: %q", st.vals[services.MetricsDBPasswordSetting])
	}

	w = httptest.NewRecorder()
	h.Get(w, httptest.NewRequest("GET", "/x", nil))
	var resp struct {
		Settings struct {
			PasswordSet bool `json:"passwordSet"`
		} `json:"settings"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Settings.PasswordSet {
		t.Fatal("the form would still show a password as stored after it was cleared")
	}
}
