package services

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dylaris-core/store"
)

// Where the long-term statistics are written, as an operator configures it.
//
// Two places can answer that, and they are not equal: METRICS_DB_URL in the
// environment WINS and makes the panel form read-only. A deployment that sets it
// (this one does) states its database in the stack file next to every other
// service, and a panel that could quietly override it would mean the file no
// longer describes what is running. The panel is for installations that did not.
const (
	MetricsDBModeSetting     = "metrics_db_mode"
	MetricsDBHostSetting     = "metrics_db_host"
	MetricsDBPortSetting     = "metrics_db_port"
	MetricsDBNameSetting     = "metrics_db_name"
	MetricsDBUserSetting     = "metrics_db_user"
	MetricsDBPasswordSetting = "metrics_db_password"
	MetricsDBSSLModeSetting  = "metrics_db_sslmode"
)

// The two modes. "core" is the default and needs no other field.
const (
	MetricsDBModeCore     = "core"
	MetricsDBModeSeparate = "separate"
)

// MetricsDBTarget is the panel-configurable half. Empty Mode means core.
type MetricsDBTarget struct {
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Port     string `json:"port"`
	DBName   string `json:"dbName"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	SSLMode  string `json:"sslMode"`
}

// IsSeparate reports whether this target names a database of its own.
func (t MetricsDBTarget) IsSeparate() bool {
	return strings.TrimSpace(t.Mode) == MetricsDBModeSeparate
}

// Normalize trims every field and fills the defaults a form leaves empty.
func (t MetricsDBTarget) Normalize() MetricsDBTarget {
	t.Mode = strings.TrimSpace(t.Mode)
	if t.Mode != MetricsDBModeSeparate {
		t.Mode = MetricsDBModeCore
	}
	t.Host = strings.TrimSpace(t.Host)
	t.Port = strings.TrimSpace(t.Port)
	t.DBName = strings.TrimSpace(t.DBName)
	t.User = strings.TrimSpace(t.User)
	t.SSLMode = strings.TrimSpace(t.SSLMode)
	if t.Port == "" {
		t.Port = "5432"
	}
	if t.SSLMode == "" {
		// Matches the rest of this platform's in-Docker connections, and is what
		// a service name like `metricsdb` on an overlay can actually offer.
		t.SSLMode = "disable"
	}
	// The password is deliberately NOT trimmed: a trailing space is a legal
	// character in one, and silently removing it produces an auth failure the
	// operator cannot see in the form.
	return t
}

// Validate reports what a form is missing. The password is optional on purpose:
// a database reached over a private network can legitimately have none, which is
// how the reference deployment runs it.
func (t MetricsDBTarget) Validate() error {
	if !t.IsSeparate() {
		return nil
	}
	if t.Host == "" {
		return fmt.Errorf("a host is required for a separate database")
	}
	if t.DBName == "" {
		return fmt.Errorf("a database name is required for a separate database")
	}
	if t.User == "" {
		return fmt.Errorf("a user is required for a separate database")
	}
	if p, err := strconv.Atoi(t.Port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("port must be a number between 1 and 65535")
	}
	return nil
}

// DSN renders what metrics.Open takes. Empty means "the Core database", which is
// exactly what Open already treats as the shared-database case, so there is no
// second way of saying it.
//
// lib/pq accepts this keyword form as readily as a URL, and it is the form the
// DB-migration screen already uses - one less place where a password has to be
// percent-encoded correctly.
func (t MetricsDBTarget) DSN() string {
	if !t.IsSeparate() {
		return ""
	}
	n := t.Normalize()
	return DBConnParams{
		Host: n.Host, Port: n.Port, User: n.User,
		Password: n.Password, DBName: n.DBName, SSLMode: n.SSLMode,
	}.DSN()
}

// LoadMetricsDBTarget reads the stored target, password included.
//
// A missing row is not an error and not a fault: it means nobody has configured
// this, and the Core database is the answer. That is the same shape as every
// other unset setting here.
func LoadMetricsDBTarget(st store.Store) MetricsDBTarget {
	get := func(k string) string {
		v, _ := st.GetSetting(k)
		return v
	}
	return MetricsDBTarget{
		Mode:     get(MetricsDBModeSetting),
		Host:     get(MetricsDBHostSetting),
		Port:     get(MetricsDBPortSetting),
		DBName:   get(MetricsDBNameSetting),
		User:     get(MetricsDBUserSetting),
		Password: get(MetricsDBPasswordSetting),
		SSLMode:  get(MetricsDBSSLModeSetting),
	}.Normalize()
}

// SaveMetricsDBTarget persists it. The password is written like any other field
// here; what protects it is that the GET never emits it.
func SaveMetricsDBTarget(st store.Store, t MetricsDBTarget) error {
	n := t.Normalize()
	for _, kv := range []struct{ k, v string }{
		{MetricsDBModeSetting, n.Mode},
		{MetricsDBHostSetting, n.Host},
		{MetricsDBPortSetting, n.Port},
		{MetricsDBNameSetting, n.DBName},
		{MetricsDBUserSetting, n.User},
		{MetricsDBPasswordSetting, n.Password},
		{MetricsDBSSLModeSetting, n.SSLMode},
	} {
		if err := st.SetSetting(kv.k, kv.v); err != nil {
			return fmt.Errorf("save %s: %w", kv.k, err)
		}
	}
	return nil
}

// EffectiveMetricsDSN is the one answer to "where does the recorder write".
//
// envURL wins over everything stored. Keep this the single place that decides,
// so boot and a settings save can never reach different conclusions - which
// would show the panel one database while another is being written.
func EffectiveMetricsDSN(envURL string, t MetricsDBTarget) string {
	if strings.TrimSpace(envURL) != "" {
		return strings.TrimSpace(envURL)
	}
	return t.DSN()
}

// MetricsDBProbe is what a test button learns about a target.
type MetricsDBProbe struct {
	Reachable bool   `json:"reachable"`
	Timescale bool   `json:"timescale"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// probeTimeout bounds the test button. Long enough for a cold container to
// answer, short enough that a wrong host does not look like a hung panel.
const probeTimeout = 8 * time.Second

// ProbeMetricsDB opens the target, asks it what it is, and closes it again.
//
// It reports rather than judges: whether the extension is there decides how the
// data is STORED, not whether the target works, and the two mistakes an operator
// can make here have opposite consequences. See the handler for which of them is
// worth refusing a save over.
func ProbeMetricsDB(ctx context.Context, t MetricsDBTarget) MetricsDBProbe {
	n := t.Normalize()
	db, err := DBConnParams{
		Host: n.Host, Port: n.Port, User: n.User,
		Password: n.Password, DBName: n.DBName, SSLMode: n.SSLMode,
	}.Open(ctx, probeTimeout)
	if err != nil {
		return MetricsDBProbe{Error: err.Error()}
	}
	defer db.Close()
	return MetricsDBProbe{Reachable: true, Timescale: probeTimescale(ctx, db), Version: probeVersion(ctx, db)}
}

// probeTimescale asks whether the extension is INSTALLED in this database, not
// whether the server could install it. Available-but-absent is the state a
// Supabase Postgres is in, and it is the one that silently produces a plain
// table.
func probeTimescale(ctx context.Context, db *sql.DB) bool {
	q, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var ok bool
	if err := db.QueryRowContext(q,
		`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`).Scan(&ok); err != nil {
		return false
	}
	return ok
}

func probeVersion(ctx context.Context, db *sql.DB) string {
	q, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	var v string
	if err := db.QueryRowContext(q, `SHOW server_version`).Scan(&v); err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}
