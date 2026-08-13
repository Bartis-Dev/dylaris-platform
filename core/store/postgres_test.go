package store

import (
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// GetServerByUUID joins nodes for node_status/node_last_seen_at so the panel
// can show honest node-vs-server connectivity instead of relying on
// servers.status, which freezes at its last node-pushed value once the node
// goes away. Confirms the join lands on the right scan targets.
func TestGetServerByUUID_ScansNodeStatusAndLastSeen(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	now := time.Now()
	cols := []string{
		"id", "uuid", "name", "node_id", "node_name", "owner_id", "owner_name", "game_image",
		"port", "memory", "cpu_limit", "start_command", "status", "desired_state", "is_fixed",
		"active_sub_server", "extra_jvm_flags", "created_at", "installer_type", "minecraft_version",
		"build_number", "disk_limit", "server_type", "proxy_id", "node_address", "host_port",
		"container_port", "cpu_pinning_mode", "cpuset", "node_status", "node_last_seen_at",
	}
	rows := sqlmock.NewRows(cols).AddRow(
		1, "uuid-a", "alpha", 7, "node-1", "owner-1", "owner-name", "itzg/minecraft-server",
		25565, 1024, 1.5, "", "online", "running", false,
		"", "", now, "", "",
		"", int64(0), "game", nil, "10.0.0.5", 25565,
		25565, "shared", "", "offline", now,
	)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE s.uuid = $1")).
		WithArgs("uuid-a").
		WillReturnRows(rows)

	got, err := s.GetServerByUUID("uuid-a")
	if err != nil {
		t.Fatalf("GetServerByUUID: %v", err)
	}
	if got.NodeStatus != "offline" {
		t.Fatalf("NodeStatus = %q, want offline", got.NodeStatus)
	}
	if got.NodeLastSeenAt == nil {
		t.Fatal("NodeLastSeenAt must be populated from the join")
	}
	if !got.NodeLastSeenAt.Equal(now) {
		t.Fatalf("NodeLastSeenAt = %v, want %v", got.NodeLastSeenAt, now)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// A node that has never reported (never in nodes.last_seen_at) must surface
// as a nil NodeLastSeenAt rather than a fabricated zero time - the panel
// distinguishes "never seen" from "seen a long time ago".
func TestGetServerByUUID_NilLastSeenWhenNodeNeverReported(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	cols := []string{
		"id", "uuid", "name", "node_id", "node_name", "owner_id", "owner_name", "game_image",
		"port", "memory", "cpu_limit", "start_command", "status", "desired_state", "is_fixed",
		"active_sub_server", "extra_jvm_flags", "created_at", "installer_type", "minecraft_version",
		"build_number", "disk_limit", "server_type", "proxy_id", "node_address", "host_port",
		"container_port", "cpu_pinning_mode", "cpuset", "node_status", "node_last_seen_at",
	}
	rows := sqlmock.NewRows(cols).AddRow(
		1, "uuid-b", "bravo", 7, "node-1", "owner-1", "owner-name", "itzg/minecraft-server",
		25565, 1024, 1.5, "", "online", "running", false,
		"", "", time.Now(), "", "",
		"", int64(0), "game", nil, "10.0.0.5", 25565,
		25565, "shared", "", "offline", nil,
	)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE s.uuid = $1")).
		WithArgs("uuid-b").
		WillReturnRows(rows)

	got, err := s.GetServerByUUID("uuid-b")
	if err != nil {
		t.Fatalf("GetServerByUUID: %v", err)
	}
	if got.NodeLastSeenAt != nil {
		t.Fatalf("NodeLastSeenAt = %v, want nil for a node that never reported", got.NodeLastSeenAt)
	}
}

// ListServersForUser's non-admin UNION-ALL branches append role/permissions
// AFTER the shared serverCols columns, so NodeStatus/NodeLastSeenAt must be
// scanned right after Region and BEFORE role/permissions - not tacked onto
// the very end, which would silently shift role/permissions into the wrong
// values. This pins that ordering down for the non-admin (owner/invited/
// inherited UNION) path.
func TestListServersForUser_NonAdminScansNodeStatusBeforeRoleAndPermissions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	s := NewPostgresStore(db)

	owner := "11111111-1111-1111-1111-111111111111"
	now := time.Now()
	cols := []string{
		"id", "uuid", "name", "node_name", "owner_name", "port", "status", "desired_state",
		"game_image", "is_fixed", "active_sub_server", "created_at", "owner_id", "memory",
		"cpu_limit", "node_id", "extra_jvm_flags", "start_command", "installer_type",
		"minecraft_version", "build_number", "disk_limit", "server_type", "proxy_id",
		"node_address", "host_port", "container_port", "region", "node_status",
		"node_last_seen_at", "role", "permissions",
	}
	rows := sqlmock.NewRows(cols).AddRow(
		5, "uuid-c", "charlie", "node-1", "owner-name", 25565, "online", "running",
		"itzg/minecraft-server", false, "", now, owner, 1024,
		1.5, 7, "", "", "",
		"", "", int64(0), "game", nil,
		"10.0.0.5", 25565, 25565, "default", "offline",
		now, "owner", nil,
	)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE s.owner_id = $1")).
		WithArgs(owner).
		WillReturnRows(rows)

	got, err := s.ListServersForUser(owner, false)
	if err != nil {
		t.Fatalf("ListServersForUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d servers, want 1", len(got))
	}
	if got[0].NodeStatus != "offline" {
		t.Fatalf("NodeStatus = %q, want offline", got[0].NodeStatus)
	}
	if got[0].NodeLastSeenAt == nil {
		t.Fatal("NodeLastSeenAt must be populated from the join")
	}
	// Proves the insertion did not shift role/permissions out of position:
	// if NodeStatus/NodeLastSeenAt were appended at the end instead of right
	// after Region, this would scan "owner" or NULL into the wrong field.
	if got[0].Role != "owner" {
		t.Fatalf("Role = %q, want owner (role/permissions must still scan correctly after the NodeStatus/NodeLastSeenAt insertion)", got[0].Role)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
