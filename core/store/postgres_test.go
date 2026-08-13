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
