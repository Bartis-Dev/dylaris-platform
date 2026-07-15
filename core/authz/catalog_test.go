package authz

import "testing"

// validVerbs mirrors the Verb constants; the integrity test rejects any
// capability whose Verb is outside this set.
var validVerbs = map[Verb]bool{
	VerbRead: true, VerbWrite: true, VerbDelete: true, VerbCreate: true,
	VerbRestore: true, VerbStart: true, VerbStop: true, VerbRestart: true,
	VerbKill: true, VerbExec: true, VerbAccess: true, VerbUse: true,
	VerbManage: true, VerbSend: true,
}

var validScopes = map[Scope]bool{ScopePanel: true, ScopeServer: true, ScopeOwner: true}

func TestCatalogIntegrity(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("catalog is empty")
	}

	seen := map[string]bool{}
	for _, c := range all {
		if c.ID == "" {
			t.Errorf("capability with empty ID: %+v", c)
		}
		if seen[c.ID] {
			t.Errorf("duplicate capability ID: %q", c.ID)
		}
		seen[c.ID] = true
		if !validScopes[c.Scope] {
			t.Errorf("capability %q has invalid scope %q", c.ID, c.Scope)
		}
		if !validVerbs[c.Verb] {
			t.Errorf("capability %q has invalid verb %q", c.ID, c.Verb)
		}
		if c.Label == "" || c.Category == "" {
			t.Errorf("capability %q missing label/category", c.ID)
		}
	}
}

func TestCatalogCRUDVerbsPresent(t *testing.T) {
	// Where CRUD applies, read/write/delete must all be assignable.
	for _, res := range []string{"files", "members", "mods"} {
		for _, v := range []string{"read", "write", "delete"} {
			id := res + "." + v
			if !Has(id) {
				t.Errorf("expected CRUD capability %q in catalog", id)
			}
		}
	}
	// Backups use create/delete/restore alongside read.
	for _, id := range []string{"backups.read", "backups.create", "backups.delete", "backups.restore"} {
		if !Has(id) {
			t.Errorf("expected backups capability %q in catalog", id)
		}
	}
}

func TestCatalogActionVerbsPresent(t *testing.T) {
	for _, id := range []string{
		"power.start", "power.stop", "power.restart", "power.kill",
		"rcon.exec", "console.read", "console.send", "sftp.access", "spark.use",
	} {
		if !Has(id) {
			t.Errorf("expected action capability %q in catalog", id)
		}
	}
}

func TestGetUnknownReturnsFalse(t *testing.T) {
	if _, ok := Get("does.not.exist"); ok {
		t.Fatal("Get on unknown id must return ok=false")
	}
}

func TestByScopeFiltersToScope(t *testing.T) {
	for _, c := range ByScope(ScopePanel) {
		if c.Scope != ScopePanel {
			t.Errorf("ByScope(panel) returned %q with scope %q", c.ID, c.Scope)
		}
	}
	if len(ByScope(ScopeServer)) == 0 {
		t.Fatal("ByScope(server) returned nothing")
	}
}

func TestGroupedScopeOrderAndContent(t *testing.T) {
	g := Grouped()
	if len(g) != 3 {
		t.Fatalf("Grouped() returned %d scopes, want 3", len(g))
	}
	wantOrder := []string{"server", "owner", "panel"}
	for i, s := range g {
		if s.Scope != wantOrder[i] {
			t.Fatalf("Grouped()[%d].Scope = %q, want %q", i, s.Scope, wantOrder[i])
		}
	}
	// A known cap must surface under server -> Files.
	found := false
	for _, s := range g {
		if s.Scope != "server" {
			continue
		}
		for _, cat := range s.Categories {
			for _, c := range cat.Capabilities {
				if c.ID == "files.read" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("Grouped() did not surface files.read under server scope")
	}
}
