package services

import (
	"os"
	"path/filepath"
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

// The settings table is the ONLY source. There was an environment variable
// beside it that won where it was set, so the same question had two answers and
// the panel could show a target that was not the one being written. For a
// setting whose wrong value silently changes the resolution of history nobody
// can backfill, that was the wrong trade.
//
// The site is named exactly: config.go's reader is the only way a variable
// could come back, so its absence there IS the invariant.
func TestThereIsNoMetricsDatabaseEnvironmentVariable(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "config", "config.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "METRICS_DB_URL") {
		t.Fatal("config.go reads METRICS_DB_URL again; the metrics target is a panel setting " +
			"and a second source can silently point recording somewhere the panel does not show")
	}
}

// What boot uses is exactly what the settings screen wrote - one function, one
// row, no second opinion.
func TestTheStoredTargetIsWhatBootApplies(t *testing.T) {
	st := &metricsSettingsStore{vals: map[string]string{}}
	want := MetricsDBTarget{
		Mode: MetricsDBModeSeparate, Host: "metricsdb", Port: "5432",
		DBName: "dylaris_metrics", User: "metrics", SSLMode: "disable",
	}
	if err := SaveMetricsDBTarget(st, want); err != nil {
		t.Fatal(err)
	}
	// Quoted: an unquoted value ends at whitespace, which is how an empty
	// password used to eat the dbname after it.
	if dsn := LoadMetricsDBTarget(st).DSN(); !strings.Contains(dsn, "host='metricsdb'") {
		t.Fatalf("boot would apply %q, which is not what was saved", dsn)
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
