package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func metricsDBHandlerFor(st *metricsDBStore, env string) *MetricsDBHandler {
	return NewMetricsDBHandler(&AppState{Store: st, MetricsDBURLFromEnv: env})
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
	h := metricsDBHandlerFor(&metricsDBStore{vals: storedSeparate()}, "")
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

// With METRICS_DB_URL set, the stack file is the authority. The panel says so
// and refuses rather than storing a target that would never be used.
func TestTheEnvironmentLocksTheForm(t *testing.T) {
	st := &metricsDBStore{vals: map[string]string{}}
	h := metricsDBHandlerFor(st, "postgres://metrics@metricsdb:5432/dylaris_metrics")

	w := httptest.NewRecorder()
	h.Get(w, httptest.NewRequest("GET", "/x", nil))
	var got struct {
		Settings struct {
			ManagedByEnv bool `json:"managedByEnv"`
		} `json:"settings"`
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Settings.ManagedByEnv {
		t.Error("managedByEnv is false, so the form would render editable over a setting it cannot change")
	}

	w = httptest.NewRecorder()
	h.Set(w, httptest.NewRequest("PUT", "/x", strings.NewReader(`{"mode":"core"}`)))
	if w.Code != http.StatusConflict {
		t.Fatalf("PUT under an env override returned %d, want 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "METRICS_DB_URL") {
		t.Errorf("the refusal does not name the variable to change: %s", w.Body.String())
	}
	if len(st.vals) != 0 {
		t.Errorf("the refused PUT still wrote settings: %v", st.vals)
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
			h := metricsDBHandlerFor(&metricsDBStore{vals: storedSeparate()}, "")
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
	h := metricsDBHandlerFor(&metricsDBStore{vals: map[string]string{}, timescal: true}, "")
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
	h := metricsDBHandlerFor(&metricsDBStore{vals: map[string]string{}}, "")
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
	h := metricsDBHandlerFor(&metricsDBStore{vals: map[string]string{}}, "")
	got := h.activeState()
	if got.Recording || got.Resolution != "" {
		t.Fatalf("activeState with no manager = %+v; want the empty state", got)
	}
}
