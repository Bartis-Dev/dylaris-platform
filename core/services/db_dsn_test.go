package services

import (
	"strings"
	"testing"
)

// A DSN value must not be able to eat the field after it.
//
// libpq's keyword/value format ends an unquoted value at whitespace, and two
// perfectly ordinary passwords used to walk straight into that:
//
//   - EMPTY. `password= dbname=x` makes libpq skip the space and take
//     `dbname=x` as the password. dbname is then never set at all, libpq
//     defaults it to the USER name, and the operator is told
//     `database "metrics" does not exist` - naming a database nobody typed,
//     about a field that was filled in correctly, caused by a different field
//     being blank. Hit in production on 2026-09-03 against a trust-auth
//     metrics database, where an empty password is the CORRECT configuration.
//   - CONTAINING A SPACE. `password=a b dbname=x` fails with
//     `missing "=" after "b"`, quoting a fragment of the password back at you.
//
// Both are the same defect and quoting closes both. These cases are string
// assertions rather than connections on purpose: the mistake is in what gets
// rendered, and a test that needs a database to notice would not run here.
func TestADSNValueCannotSwallowTheNextField(t *testing.T) {
	base := DBConnParams{
		Host: "metricsdb", Port: "5432", User: "metrics",
		DBName: "dylaris_metrics", SSLMode: "disable",
	}

	for _, pw := range []string{"", "1", "a b", "  ", "pa ss word", `back\slash`, "quo'te"} {
		p := base
		p.Password = pw
		dsn := p.DSN()
		// The field after the password is the one that gets eaten, so it is the
		// one worth asserting on.
		if !strings.Contains(dsn, "dbname='dylaris_metrics'") {
			t.Errorf("password %q produced a dsn without an intact dbname:\n  %s", pw, dsn)
		}
		if !strings.Contains(dsn, "sslmode='disable'") {
			t.Errorf("password %q produced a dsn without an intact sslmode:\n  %s", pw, dsn)
		}
	}
}

// An empty value has to be RENDERED, not omitted. Leaving the keyword out
// entirely would be the other way to break it: libpq would then fall back to
// PGPASSWORD or .pgpass, so a form that says "no password" would quietly use
// whatever the environment happens to hold.
func TestAnEmptyValueIsWrittenAsAnEmptyValue(t *testing.T) {
	dsn := DBConnParams{Host: "h", Port: "5432", User: "u", DBName: "d"}.DSN()
	if !strings.Contains(dsn, "password=''") {
		t.Fatalf("an empty password is not rendered as an empty value: %s", dsn)
	}
}

// Escaping, so a password made of the quoting characters themselves still
// round-trips rather than terminating its own value early.
func TestQuotingEscapesTheQuotingCharacters(t *testing.T) {
	cases := map[string]string{
		"":            `''`,
		"plain":       `'plain'`,
		"a b":         `'a b'`,
		"it's":        `'it\'s'`,
		`back\slash`:  `'back\\slash'`,
		`both'and\it`: `'both\'and\\it'`,
	}
	for in, want := range cases {
		if got := quoteDSNValue(in); got != want {
			t.Errorf("quoteDSNValue(%q) = %s, want %s", in, got, want)
		}
	}
}

// SSL mode still defaults, and still through the quoting - a default that
// bypassed it would be an unquoted value hiding among quoted ones.
func TestTheSSLModeDefaultIsQuotedToo(t *testing.T) {
	dsn := DBConnParams{Host: "h", Port: "5432", User: "u", DBName: "d"}.DSN()
	if !strings.Contains(dsn, "sslmode='disable'") {
		t.Fatalf("sslmode default is missing or unquoted: %s", dsn)
	}
}
