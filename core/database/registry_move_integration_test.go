package database

import (
	"testing"
)

// The migration is a prefix rewrite over live rows, so it is tested against a
// REAL Postgres like the rest of this file's neighbours - a fake would only
// prove that Go string handling works, and the risk here is the SQL.
//
// What it has to get right, and each of these has a way to be wrong:
//
//   - rewrite an old path, keeping the suffix AND the tag intact;
//   - leave a path that already names the new registry alone, rather than
//     prefixing it twice;
//   - leave a completely unrelated image alone, because operators may run their
//     own runtime images and this migration is not entitled to touch them;
//   - do nothing on a second run, since it executes on every boot.
func TestIntegrationRegistryMoveRewritesOnlyTheOldPrefix(t *testing.T) {
	db, st := integrationDB(t)
	f := newFixture(t, st)

	cases := []struct {
		name  string
		start string
		want  string
	}{
		{
			"old path is rewritten with suffix and tag intact",
			"ghcr.io/bartis-dev/dylaris-platform-mc-java21:latest",
			"ghcr.io/dylaris-dev/platform-mc-java21:latest",
		},
		{
			"a pinned digest survives the rewrite",
			"ghcr.io/bartis-dev/dylaris-platform-mc-java8@sha256:abc123",
			"ghcr.io/dylaris-dev/platform-mc-java8@sha256:abc123",
		},
		{
			"a path already on the new registry is left alone",
			"ghcr.io/dylaris-dev/platform-mc-java17:latest",
			"ghcr.io/dylaris-dev/platform-mc-java17:latest",
		},
		{
			"an operator's own image is not ours to rewrite",
			"docker.io/itzg/minecraft-server:latest",
			"docker.io/itzg/minecraft-server:latest",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE servers SET game_image = $1 WHERE id = $2`, c.start, f.server.ID); err != nil {
				t.Fatalf("seed game_image: %v", err)
			}
			if err := applyRegistryMoveSchema(db); err != nil {
				t.Fatalf("applyRegistryMoveSchema: %v", err)
			}
			var got string
			if err := db.QueryRow(`SELECT game_image FROM servers WHERE id = $1`, f.server.ID).Scan(&got); err != nil {
				t.Fatalf("read back: %v", err)
			}
			if got != c.want {
				t.Errorf("game_image = %q, want %q", got, c.want)
			}

			// Second run: it executes on every boot, so a rewrite that is not
			// idempotent would keep eating the prefix on each restart.
			if err := applyRegistryMoveSchema(db); err != nil {
				t.Fatalf("second run: %v", err)
			}
			if err := db.QueryRow(`SELECT game_image FROM servers WHERE id = $1`, f.server.ID).Scan(&got); err != nil {
				t.Fatalf("read back after second run: %v", err)
			}
			if got != c.want {
				t.Errorf("second run changed it again: %q, want %q", got, c.want)
			}
		})
	}
}
