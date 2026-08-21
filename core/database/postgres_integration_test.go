package database

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dylaris-core/config"
	"dylaris-core/models"
	"dylaris-core/store"
)

// Integration tests against a REAL Postgres. Every other test in this repo uses
// fakes, which is why five defects this session were invisible to the suite:
// they were all SQL that only Postgres rejects.
//
//	ticket status / user role   inconsistent types deduced for parameter $1
//	ticket_messages.user_id     NOT NULL with ON DELETE SET NULL -> 23502
//	backup_jobs.*_patterns      nil slice into a NOT NULL text[] -> 23502
//	spark_profiles.requested_by UUID scanned into sql.NullInt64
//
// Skipped unless DYLARIS_TEST_DB_HOST is set, so `go test ./...` stays green on
// a machine with no database. CI provides a postgres service container.
//
// Run locally:
//
//	docker run -d --name dyl-pgtest -e POSTGRES_PASSWORD=testpw \
//	  -e POSTGRES_USER=dylaris -e POSTGRES_DB=dylaris_test -p 55432:5432 postgres:16-alpine
//	DYLARIS_TEST_DB_HOST=127.0.0.1 DYLARIS_TEST_DB_PORT=55432 \
//	  DYLARIS_TEST_DB_USER=dylaris DYLARIS_TEST_DB_PASSWORD=testpw \
//	  DYLARIS_TEST_DB_NAME=dylaris_test go test ./database/ -run Integration -v
var uniqueSuffix atomic.Int64

// runTag makes every generated name unique per test PROCESS, not just per
// call. The tables outlive the run (the CI service container is fresh, a local
// container is not), and a counter restarting at 1 collides with rows a
// previous run left behind on a UNIQUE column.
var runTag = strconv.FormatInt(time.Now().UnixNano(), 36)

// uniqueName returns a name no other test or run will produce.
func uniqueName(prefix string) string {
	return prefix + runTag + "_" + strconv.FormatInt(uniqueSuffix.Add(1), 10)
}

// testDBConfig builds the connection config from the environment, skipping the
// calling test when no database is configured.
func testDBConfig(t *testing.T) config.Config {
	t.Helper()
	host := os.Getenv("DYLARIS_TEST_DB_HOST")
	if host == "" {
		t.Skip("DYLARIS_TEST_DB_HOST not set - skipping the Postgres integration tests")
	}
	env := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	return config.Config{
		DBHost:     host,
		DBPort:     env("DYLARIS_TEST_DB_PORT", "5432"),
		DBUser:     env("DYLARIS_TEST_DB_USER", "postgres"),
		DBPassword: os.Getenv("DYLARIS_TEST_DB_PASSWORD"),
		DBName:     env("DYLARIS_TEST_DB_NAME", "postgres"),
		DBSSLMode:  "disable",
		// Plain Postgres, not TimescaleDB: server_stats is then created as an
		// ordinary table, so no extension is needed in the test image.
		DBType: "postgres",
	}
}

// freshSchemaDB creates a scratch database, builds the schema on it EXACTLY
// once and hands back the handle. Dropped again on cleanup.
//
// One boot is what a fresh install is, and it is not the same thing as the
// shared test database: migrateSchema's ADD COLUMN set runs before the later
// phases create their tables, so a column whose table does not exist yet is
// missing after the first boot and present after the second. A test that wants
// to see that has to start from nothing.
func freshSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	cfg := testDBConfig(t)

	admin, err := sql.Open("postgres", fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName))
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	defer admin.Close()

	name := "fresh_" + runTag
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatalf("create scratch database %s: %v", name, err)
	}

	cfg.DBName = name
	db, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB on the scratch database: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		dropper, err := sql.Open("postgres", fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
			cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, testDBName()))
		if err != nil {
			return
		}
		defer dropper.Close()
		if _, err := dropper.Exec("DROP DATABASE IF EXISTS " + name); err != nil {
			t.Logf("could not drop the scratch database %s: %v", name, err)
		}
	})
	return db
}

func testDBName() string {
	if v := os.Getenv("DYLARIS_TEST_DB_NAME"); v != "" {
		return v
	}
	return "postgres"
}

func integrationDB(t *testing.T) (*sql.DB, *store.PostgresStore) {
	t.Helper()
	cfg := testDBConfig(t)
	db, err := InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB against the test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, store.NewPostgresStore(db)
}

// fixture creates the rows the tests hang off: a user, a node and a server.
// Names carry a per-call suffix so a repeated local run does not collide with
// the UNIQUE constraints, and everything is removed again on cleanup.
type fixture struct {
	user   *models.User
	node   *models.Node
	server *models.Server
}

func newFixture(t *testing.T, st *store.PostgresStore) *fixture {
	t.Helper()
	tag := strings.ToLower(strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	if len(tag) > 20 {
		tag = tag[:20]
	}
	suffix := func(p string) string { return uniqueName(p + tag + "_") }

	u := &models.User{Username: suffix("u_"), Password: "x", Email: suffix("e_") + "@example.test"}
	if err := st.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	n := &models.Node{Name: suffix("n_"), Address: "127.0.0.1", Token: suffix("t_"), Status: "online"}
	if err := st.CreateNode(n); err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	srv := &models.Server{
		UUID: suffix("uuid_"), Name: suffix("s_"), NodeID: n.ID, OwnerID: u.ID,
		GameImage: "img", Port: 25600, Memory: 1024, Status: "stopped", ServerType: "game",
	}
	sid, err := st.CreateServer(srv)
	if err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	srv.ID = int(sid)

	t.Cleanup(func() {
		st.DeleteServer(srv.ID)
		st.DeleteNode(n.ID)
		st.DeleteUser(u.ID)
	})
	return &fixture{user: u, node: n, server: srv}
}

// A create request that omits includePatterns/excludePatterns is legal and
// answered 500: a nil slice reaches the driver as NULL, and naming the column
// in the INSERT means its DEFAULT '{}' never applies.
func TestIntegrationBackupJobWithoutPatterns(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	job := &models.BackupJob{ServerID: f.server.ID, Name: "no patterns", Schedule: "manual", RetentionCount: 3, Enabled: true}
	id, err := st.CreateBackupJob(job)
	if err != nil {
		t.Fatalf("CreateBackupJob with nil patterns: %v", err)
	}
	t.Cleanup(func() { st.DeleteBackupJob(id) })

	got, err := st.GetBackupJob(id)
	if err != nil {
		t.Fatalf("GetBackupJob: %v", err)
	}
	if got.IncludePatterns == nil || len(got.IncludePatterns) != 0 {
		t.Errorf("IncludePatterns = %#v, want an empty list", got.IncludePatterns)
	}

	job.ID = id
	job.Name = "still no patterns"
	if err := st.UpdateBackupJob(job); err != nil {
		t.Errorf("UpdateBackupJob with nil patterns: %v", err)
	}
}

// Deleting a user who ever wrote a ticket message hit a not-null violation:
// ticket_messages.user_id was NOT NULL with an ON DELETE SET NULL reference.
//
// The ticket is owned by someone ELSE, which is the case that matters: a
// support agent who replied on a foreign ticket. Deleting the ticket's own
// owner cascades the whole thread away (tickets.user_id is ON DELETE CASCADE),
// so that path never exercises the SET NULL at all.
func TestIntegrationDeleteUserWhoWroteATicketMessage(t *testing.T) {
	_, st := integrationDB(t)
	owner := newFixture(t, st) // owns a server, so it is never the deleted user

	cat := &models.TicketCategory{Name: uniqueName("cat_"), Enabled: true, DefaultPriority: "normal"}
	catID, err := st.CreateTicketCategory(cat)
	if err != nil {
		t.Fatalf("CreateTicketCategory: %v", err)
	}
	t.Cleanup(func() { st.DeleteTicketCategory(catID) })

	// The author owns no server: DeleteUser deliberately refuses to remove a
	// user who still owns one, so the fixture user cannot be the one deleted.
	// The replying agent owns nothing, so DeleteUser's "still owns servers"
	// guard does not fire.
	author := &models.User{
		Username: uniqueName("author_"),
		Password: "x",
		Email:    uniqueName("author_") + "@example.test",
	}
	if err := st.CreateUser(author); err != nil {
		t.Fatalf("CreateUser(author): %v", err)
	}

	tk := &models.Ticket{CategoryID: catID, UserID: owner.user.ID, Title: "help", Status: "open", Priority: "normal"}
	ticketID, err := st.CreateTicket(tk)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	if _, err := st.AddTicketMessage(&models.TicketMessage{TicketID: ticketID, UserID: author.ID, Body: "my message"}); err != nil {
		t.Fatalf("AddTicketMessage: %v", err)
	}

	if err := st.DeleteUser(author.ID); err != nil {
		t.Fatalf("DeleteUser after the user wrote a ticket message: %v", err)
	}

	msgs, err := st.ListTicketMessages(ticketID, true)
	if err != nil {
		t.Fatalf("ListTicketMessages after the author was deleted: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want the anonymised one to survive", len(msgs))
	}
	if msgs[0].Body != "my message" {
		t.Errorf("body = %q, want it unchanged", msgs[0].Body)
	}
	if msgs[0].UserID != "" {
		t.Errorf("userId = %q, want it cleared by the ON DELETE SET NULL", msgs[0].UserID)
	}
}

// Closing a ticket and changing a user's role both failed with "inconsistent
// types deduced for parameter $1" - a defect no fake can reproduce, and one
// that meant neither had ever worked.
func TestIntegrationStatementsPostgresHadRejected(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	cat := &models.TicketCategory{Name: uniqueName("cat_"), Enabled: true, DefaultPriority: "normal"}
	catID, err := st.CreateTicketCategory(cat)
	if err != nil {
		t.Fatalf("CreateTicketCategory: %v", err)
	}
	t.Cleanup(func() { st.DeleteTicketCategory(catID) })

	ticketID, err := st.CreateTicket(&models.Ticket{
		CategoryID: catID, UserID: f.user.ID, Title: "transitions", Status: "open", Priority: "normal",
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	for _, status := range []string{"in_progress", "waiting", "resolved", "closed", "open"} {
		if err := st.UpdateTicketStatus(ticketID, status); err != nil {
			t.Errorf("UpdateTicketStatus(%q): %v", status, err)
		}
	}

	for _, role := range []string{"support", "admin", "user"} {
		if err := st.SetUserRole(f.user.ID, role); err != nil {
			t.Errorf("SetUserRole(%q): %v", role, err)
		}
	}
}

// spark_profiles.requested_by is a UUID column. Scanning it into an integer
// target failed for every row, and the scan error was swallowed, so the
// endpoint answered 200 with an empty list.
func TestIntegrationSparkProfileRoundTrip(t *testing.T) {
	db, st := integrationDB(t)
	f := newFixture(t, st)

	if _, err := db.Exec(
		`INSERT INTO spark_profiles (server_id, sub_server_name, url, requested_by) VALUES ($1, $2, $3, $4)`,
		f.server.ID, "survival", "https://spark.example/x", f.user.ID,
	); err != nil {
		t.Fatalf("insert spark profile: %v", err)
	}

	rows, err := db.Query(
		`SELECT id, server_id, sub_server_name, url, requested_by FROM spark_profiles WHERE server_id = $1`,
		f.server.ID)
	if err != nil {
		t.Fatalf("query spark profiles: %v", err)
	}
	defer rows.Close()

	found := 0
	for rows.Next() {
		var id, serverID int
		var subServer, url string
		var requestedBy sql.NullString
		if err := rows.Scan(&id, &serverID, &subServer, &url, &requestedBy); err != nil {
			t.Fatalf("scan: %v - requested_by is a UUID, not an integer", err)
		}
		if !requestedBy.Valid || requestedBy.String != f.user.ID {
			t.Errorf("requested_by = %v, want %q", requestedBy, f.user.ID)
		}
		found++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if found != 1 {
		t.Errorf("got %d profiles, want 1", found)
	}
}

func TestGatewayBandwidthStatsTable(t *testing.T) {
	db := freshSchemaDB(t) // full schema on a scratch DB; skips without a test DB
	if _, err := db.Exec(`INSERT INTO gateway_bandwidth_stats (time, component, id, host, region, rx_bps, tx_bps, cap_mbit)
		VALUES (NOW(), 'warp', 'eu-1', 'web-eu-1', 'eu-central', 100, 200, 1000)`); err != nil {
		t.Fatalf("insert into gateway_bandwidth_stats: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM gateway_bandwidth_stats WHERE id='eu-1'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}
}

// A BYON node is a tenant's own machine, paired through a single-use enroll
// token. NodeCleanupService sweeps offline server-less nodes every 5 minutes
// with a 24h cutoff, and that sweep used to include owned nodes - so a customer
// who registered a node and then did not create a server for a day (a laptop, a
// home box off over a weekend) lost the pairing. Not just the row: the node's
// scoped Redis ACL users are pruned with it, and on the next boot the node still
// holds its cached .node_secret, so the "paired node with no cached secret"
// guard in redisacl_bootstrap never fires. It reconnects under an identity Core
// has forgotten and loops on a rejected handshake, with no BYON route back in.
//
// Against a real Postgres rather than sqlmock, because the whole change is one
// SQL predicate and NULL semantics are exactly what a query-text assertion
// cannot check: nodes.owner_id is nullable, and the sweep's own
// "id NOT IN (SELECT node_id FROM servers)" sits one nullable column away from
// matching nothing at all.
func TestIntegrationStaleNodeSweepSparesBYONNodes(t *testing.T) {
	db, st := integrationDB(t)
	f := newFixture(t, st) // supplies the user that owns the BYON node

	stale := time.Now().Add(-48 * time.Hour)

	mkNode := func(prefix string, owner *string) *models.Node {
		t.Helper()
		n := &models.Node{Name: uniqueName(prefix), Address: "127.0.0.1", Token: uniqueName(prefix + "t_"), Status: "offline"}
		if err := st.CreateNode(n); err != nil {
			t.Fatalf("CreateNode(%s): %v", prefix, err)
		}
		t.Cleanup(func() { st.DeleteNode(n.ID) })
		if owner != nil {
			if err := st.SetNodeOwner(n.ID, owner); err != nil {
				t.Fatalf("SetNodeOwner(%s): %v", prefix, err)
			}
		}
		if _, err := db.Exec(`UPDATE nodes SET status = 'offline', last_seen_at = $1 WHERE id = $2`, stale, n.ID); err != nil {
			t.Fatalf("age node %s: %v", prefix, err)
		}
		return n
	}

	byon := mkNode("byon_", &f.user.ID)
	platform := mkNode("plat_", nil)

	if _, err := st.DeleteStaleOfflineNodes(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatalf("DeleteStaleOfflineNodes: %v", err)
	}

	if got, err := st.GetNodeByID(byon.ID); err != nil || got == nil {
		t.Errorf("the BYON node was swept: a tenant's pairing must survive being offline (err=%v)", err)
	}
	// The operator's own unadopted node is exactly the churn the sweep exists
	// for, so sparing everything would be the opposite mistake.
	if got, _ := st.GetNodeByID(platform.ID); got != nil {
		t.Errorf("the platform node survived the sweep: the cleanup no longer cleans anything up")
	}
}

// /auth/forgot-password sends mail on an anonymous request and REPLACES the
// reset token every time. Its sibling /auth/resend-verification has enforced a
// per-mailbox cooldown for exactly that reason ("a per-IP limit bounds one
// CALLER, not one MAILBOX"); this endpoint had only the shared per-IP limiter,
// so a caller rotating source addresses could both flood an inbox and keep
// invalidating the link the victim was trying to click.
//
// The window is enforced inside the UPDATE's WHERE, against
// password_reset_expires_at - a reset token's expiry IS its send time plus the
// policy TTL, so no new column was needed. Against a real Postgres because
// that predicate, its NULL branch and RowsAffected are the entire mechanism.
func TestIntegrationPasswordResetTokenCooldown(t *testing.T) {
	_, st := integrationDB(t)
	f := newFixture(t, st)

	const ttl = 30 * time.Minute
	const cooldown = 60 * time.Second

	issue := func(at time.Time, token string) bool {
		t.Helper()
		expires := at.Add(ttl)
		ok, err := st.SetPasswordResetToken(f.user.ID, token, expires, expires.Add(-cooldown))
		if err != nil {
			t.Fatalf("SetPasswordResetToken: %v", err)
		}
		return ok
	}

	now := time.Now()

	// No token on the row yet: the NULL branch must let the first one through,
	// or password reset would never work at all.
	if !issue(now, "first-token") {
		t.Fatal("the first request was refused; the NULL branch does not let a first token through")
	}
	if issue(now.Add(5*time.Second), "flood-token") {
		t.Error("a second request 5s later was allowed: the mailbox cooldown does not hold")
	}
	if issue(now.Add(30*time.Second), "flood-token-2") {
		t.Error("a request inside the cooldown was allowed")
	}
	if !issue(now.Add(90*time.Second), "second-token") {
		t.Error("a request after the cooldown was refused; a user who never got the first mail could not ask again")
	}

	// A completed reset clears both columns, and the next request must not be
	// held back by the token that was just consumed.
	if err := st.ClearPasswordResetToken(f.user.ID); err != nil {
		t.Fatalf("ClearPasswordResetToken: %v", err)
	}
	if !issue(now.Add(91*time.Second), "after-consume") {
		t.Error("a request right after a completed reset was refused")
	}

	// The token that survives must be the last one actually issued.
	u, err := st.GetUserByPasswordResetToken("after-consume")
	if err != nil || u == nil || u.ID != f.user.ID {
		t.Errorf("the issued token does not resolve back to its user (err=%v)", err)
	}
	if u, _ := st.GetUserByPasswordResetToken("flood-token"); u != nil {
		t.Error("a token from a refused request was stored anyway")
	}
}
