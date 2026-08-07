package database

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// server_invites has three foreign keys into users. server_id and user_id were
// declared ON DELETE CASCADE; invited_by was declared with no ON DELETE clause,
// which Postgres reads as NO ACTION - and it was NOT NULL, so SET NULL was not
// available to it either. An admin who granted access on a server they did not
// own therefore became undeletable, with a 409 the operator could not act on.
//
// Proven against the live testbed database before the fix, using CreateInvite's
// own statement (invited_by = the acting user, owner_user_id derived from the
// server, so the cascade on owner_user_id does not cover it):
//
//	ERROR: update or delete on table "users" violates foreign key constraint
//	       "server_invites_invited_by_fkey" on table "server_invites"
//
// These read the schema source rather than standing up Postgres, which is what
// the rest of this package's tests do.
func TestServerInvitesAttributionIsNullableAndSetsNull(t *testing.T) {
	src, err := os.ReadFile("db_tables.go")
	if err != nil {
		t.Fatalf("read db_tables.go: %v", err)
	}
	decl := regexp.MustCompile(`(?m)^\s*invited_by\s+UUID[^,]*,`).FindString(string(src))
	if decl == "" {
		t.Fatal("server_invites.invited_by declaration not found")
	}
	if strings.Contains(decl, "NOT NULL") {
		t.Errorf("invited_by is still NOT NULL, so SET NULL cannot apply: %s", strings.TrimSpace(decl))
	}
	if !strings.Contains(decl, "ON DELETE SET NULL") {
		t.Errorf("invited_by has no ON DELETE SET NULL, so a fresh install still blocks the delete: %s", strings.TrimSpace(decl))
	}
	// CASCADE here would silently revoke a member's access because an unrelated
	// admin was deleted, which is a permission change nobody asked for.
	if strings.Contains(decl, "ON DELETE CASCADE") {
		t.Errorf("invited_by must not CASCADE - that would revoke a live grant: %s", strings.TrimSpace(decl))
	}
}

// The migration has to reach installs that already have the NOT NULL column,
// and it has to survive being run again on every boot.
func TestInviteAttributionMigrationIsIdempotent(t *testing.T) {
	src, err := os.ReadFile("db_phase24_invite_attribution.go")
	if err != nil {
		t.Fatalf("read the migration: %v", err)
	}
	// Strip comment lines first. The doc comment above the function explains
	// the drop-then-add ordering, and matching that prose instead of the SQL
	// made the ordering check below fail on correct code.
	var body []string
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		body = append(body, line)
	}
	text := strings.Join(body, "\n")

	for _, want := range []string{
		"ALTER COLUMN invited_by DROP NOT NULL",
		"DROP CONSTRAINT IF EXISTS server_invites_invited_by_fkey",
		"ON DELETE SET NULL",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("migration is missing %q", want)
		}
	}
	// ADD CONSTRAINT has no IF NOT EXISTS, so the DROP must come first or the
	// second boot fails with "constraint already exists".
	dropAt := strings.Index(text, "DROP CONSTRAINT IF EXISTS")
	addAt := strings.Index(text, "ADD CONSTRAINT")
	if dropAt < 0 || addAt < 0 || dropAt > addAt {
		t.Error("the constraint must be dropped before it is re-added, or the migration is not idempotent")
	}
}

// The naive half of this fix is worse than the bug: both readers joined the
// inviter with an INNER JOIN, so a nulled invited_by would have dropped the row
// from the members list while the access itself kept working. Nothing in the
// panel even renders inviterName - the join existed only to fill a field no UI
// shows.
func TestInviteReadersSurviveADeletedInviter(t *testing.T) {
	src, err := os.ReadFile("../store/postgres.go")
	if err != nil {
		t.Fatalf("read postgres.go: %v", err)
	}
	text := string(src)

	if n := strings.Count(text, "JOIN users inv_u ON si.invited_by = inv_u.id"); n != 2 {
		t.Fatalf("expected 2 inviter joins, found %d - the check below no longer covers them", n)
	}
	if n := strings.Count(text, "LEFT JOIN users inv_u ON si.invited_by = inv_u.id"); n != 2 {
		t.Errorf("an inviter join is still an INNER JOIN: a deleted inviter would hide a live grant")
	}
	if n := strings.Count(text, "COALESCE(inv_u.username, '')"); n != 2 {
		t.Errorf("a null inviter username is not coalesced; the scan into a string would fail")
	}
	if n := strings.Count(text, "COALESCE(si.invited_by::text, '')"); n != 2 {
		t.Errorf("a null invited_by is not coalesced; the scan into a string would fail")
	}
}
