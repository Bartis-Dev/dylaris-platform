package services

import (
	"strings"
	"testing"

	"dylaris-core/store"
)

// metricsSettingsStore is the settings table and nothing else. Embedding the
// interface means a method this code starts calling shows up as a nil-pointer
// panic naming it, rather than compiling against a silent stub.
type metricsSettingsStore struct {
	store.Store
	vals map[string]string
}

func (s *metricsSettingsStore) GetSetting(k string) (string, error) { return s.vals[k], nil }
func (s *metricsSettingsStore) SetSetting(k, v string) error        { s.vals[k] = v; return nil }

// The environment is the authority when it is set, and the reason is not
// taste: this deployment names its metrics database in the stack file beside
// every other service. A panel that could override it would make that file stop
// describing what is running, and the two answers would drift with nothing
// reporting it.
func TestTheEnvironmentBeatsTheStoredTarget(t *testing.T) {
	stored := MetricsDBTarget{
		Mode: MetricsDBModeSeparate, Host: "panel-host", Port: "5432",
		DBName: "panel_db", User: "panel_user",
	}
	got := EffectiveMetricsDSN("postgres://env@envhost/envdb", stored)
	if !strings.Contains(got, "envhost") {
		t.Fatalf("EffectiveMetricsDSN = %q; the environment must win", got)
	}
	// And with nothing in the environment, the stored one is used.
	got = EffectiveMetricsDSN("   ", stored)
	if !strings.Contains(got, "panel-host") {
		t.Fatalf("with a blank env var the stored target must be used, got %q", got)
	}
}

// Core mode has to produce the EMPTY dsn, because that is already the word
// metrics.Open uses for "record into the Core database". Inventing a second
// spelling would mean two places deciding what "core" means.
func TestCoreModeIsTheEmptyDSN(t *testing.T) {
	for _, mode := range []string{"", "core", "  core  ", "nonsense"} {
		tg := MetricsDBTarget{Mode: mode, Host: "ignored", DBName: "ignored", User: "ignored"}
		if dsn := tg.DSN(); dsn != "" {
			t.Errorf("mode %q produced dsn %q; want empty", mode, dsn)
		}
	}
	sep := MetricsDBTarget{Mode: MetricsDBModeSeparate, Host: "h", DBName: "d", User: "u"}
	if sep.DSN() == "" {
		t.Error("a separate target produced the empty dsn, which means the Core database")
	}
}

// A password is OPTIONAL. The reference deployment runs the metrics database
// with none at all, reachable only from Core on a two-member network, so a
// required-password rule here would make the documented setup unconfigurable.
func TestAPasswordIsNotRequired(t *testing.T) {
	tg := MetricsDBTarget{
		Mode: MetricsDBModeSeparate, Host: "metricsdb", DBName: "dylaris_metrics", User: "metrics",
	}.Normalize()
	if err := tg.Validate(); err != nil {
		t.Fatalf("a passwordless separate target was rejected: %v", err)
	}
	if !strings.Contains(tg.DSN(), "password=") {
		t.Error("the dsn dropped the password keyword entirely; lib/pq needs the field present")
	}
}

func TestValidateNamesWhatIsMissing(t *testing.T) {
	base := MetricsDBTarget{Mode: MetricsDBModeSeparate, Host: "h", DBName: "d", User: "u", Port: "5432"}
	cases := []struct {
		name string
		mut  func(*MetricsDBTarget)
		want string
	}{
		{"no host", func(t *MetricsDBTarget) { t.Host = "" }, "host"},
		{"no database", func(t *MetricsDBTarget) { t.DBName = "" }, "database name"},
		{"no user", func(t *MetricsDBTarget) { t.User = "" }, "user"},
		{"port is not a number", func(t *MetricsDBTarget) { t.Port = "http" }, "port"},
		{"port out of range", func(t *MetricsDBTarget) { t.Port = "70000" }, "port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tg := base
			c.mut(&tg)
			err := tg.Validate()
			if err == nil {
				t.Fatalf("%s was accepted", c.name)
			}
			if !strings.Contains(strings.ToLower(err.Error()), c.want) {
				t.Errorf("error %q does not mention %q, so the form cannot point at the field", err, c.want)
			}
		})
	}
	// The same gaps are fine in core mode - none of those fields is used.
	for _, c := range cases {
		tg := base
		tg.Mode = MetricsDBModeCore
		c.mut(&tg)
		if err := tg.Validate(); err != nil {
			t.Errorf("core mode rejected for %s: %v", c.name, err)
		}
	}
}

// A trailing space is a legal password character. Trimming it produces an
// authentication failure with a form that looks exactly right, which is the
// worst kind of wrong answer to give an operator.
func TestNormalizeTrimsEveryFieldExceptThePassword(t *testing.T) {
	tg := MetricsDBTarget{
		Mode: " separate ", Host: " h ", Port: " 6000 ", DBName: " d ", User: " u ",
		Password: " secret ", SSLMode: " require ",
	}.Normalize()
	if tg.Host != "h" || tg.Port != "6000" || tg.DBName != "d" || tg.User != "u" || tg.SSLMode != "require" {
		t.Fatalf("normalize left whitespace: %+v", tg)
	}
	if tg.Password != " secret " {
		t.Errorf("the password was trimmed to %q; a space is a legal character in one", tg.Password)
	}
	if !tg.IsSeparate() {
		t.Error("a padded mode was not recognised")
	}
}

func TestNormalizeFillsTheDefaultsAFormLeavesEmpty(t *testing.T) {
	tg := MetricsDBTarget{Mode: MetricsDBModeSeparate, Host: "h", DBName: "d", User: "u"}.Normalize()
	if tg.Port != "5432" {
		t.Errorf("port default = %q, want 5432", tg.Port)
	}
	if tg.SSLMode != "disable" {
		t.Errorf("sslmode default = %q, want disable to match the rest of this platform", tg.SSLMode)
	}
}

// A store with nothing in it must mean "the Core database", not an error and
// not a broken separate target. This is the state of every fresh install.
func TestAnUnconfiguredStoreMeansTheCoreDatabase(t *testing.T) {
	st := &metricsSettingsStore{vals: map[string]string{}}
	tg := LoadMetricsDBTarget(st)
	if tg.IsSeparate() {
		t.Fatalf("an empty store produced a separate target: %+v", tg)
	}
	if tg.DSN() != "" {
		t.Errorf("dsn = %q, want empty", tg.DSN())
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	st := &metricsSettingsStore{vals: map[string]string{}}
	want := MetricsDBTarget{
		Mode: MetricsDBModeSeparate, Host: "metricsdb", Port: "5432",
		DBName: "dylaris_metrics", User: "metrics", Password: "pw", SSLMode: "disable",
	}
	if err := SaveMetricsDBTarget(st, want); err != nil {
		t.Fatal(err)
	}
	got := LoadMetricsDBTarget(st)
	if got != want {
		t.Fatalf("round trip changed the target:\n got %+v\nwant %+v", got, want)
	}
}
