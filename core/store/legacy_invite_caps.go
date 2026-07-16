package store

import "dylaris-core/models"

// MapLegacyInviteCaps maps the legacy 9-bool TabPermissions blob to the new
// granular SERVER cap set (phase 3 migration). Field order is fixed so the
// output is deterministic. Inherit is NOT a cap (it stays the invite.inherit
// column). There was no legacy backups bit, so a migrated friend gets NO
// backups.* caps - this preserves the old behavior where backups were
// owner/admin only. Every id here is a real ScopeServer cap in
// authz/catalog.go (enforced by db_phase21_invite_migration_test.go in the
// database package, which already imports both store and authz - this
// package cannot import authz itself, since authz imports store).
//
// Exported so both the one-time boot migration (database package) and the
// live member write path (CreateInvite/UpdateInvitePermissions below) derive
// cap_overrides from the exact same mapping, keeping a migrated row and an
// inline-written row indistinguishable.
func MapLegacyInviteCaps(p models.TabPermissions) []string {
	caps := []string{}
	if p.Console {
		caps = append(caps, "console.read", "console.send")
	}
	if p.Files {
		caps = append(caps, "files.read", "files.write", "files.delete")
	}
	if p.Config {
		caps = append(caps, "config.read", "config.write", "mods.read", "mods.write", "mods.delete")
	}
	if p.Power {
		caps = append(caps, "power.start", "power.stop", "power.restart", "power.kill", "rcon.exec", "players.read", "players.manage")
	}
	if p.Network {
		caps = append(caps, "network.read", "network.write")
	}
	if p.Overview {
		caps = append(caps, "overview.read")
	}
	if p.Members {
		caps = append(caps, "members.read", "members.write", "members.delete")
	}
	if p.Setup {
		caps = append(caps, "server.settings.write")
	}
	return caps
}
