package authz

import "testing"

// TestPresetsIntegrity: every preset cap is a real SERVER-scope catalog cap,
// ids are unique, and no cap is listed twice in one preset. This is the
// standing guarantee that the presets never drift from the catalog.
func TestPresetsIntegrity(t *testing.T) {
	ps := Presets()
	if len(ps) == 0 {
		t.Fatal("no presets defined")
	}
	seenID := map[string]bool{}
	for _, p := range ps {
		if p.ID == "" || p.Label == "" || p.Description == "" {
			t.Errorf("preset %+v missing id/label/description", p)
		}
		if seenID[p.ID] {
			t.Errorf("duplicate preset id %q", p.ID)
		}
		seenID[p.ID] = true
		if len(p.Capabilities) == 0 {
			t.Errorf("preset %q has no capabilities", p.ID)
		}
		seenCap := map[string]bool{}
		for _, capID := range p.Capabilities {
			c, ok := Get(capID)
			if !ok {
				t.Errorf("preset %q references unknown capability %q", p.ID, capID)
				continue
			}
			if c.Scope != ScopeServer {
				t.Errorf("preset %q cap %q has scope %q, want server", p.ID, capID, c.Scope)
			}
			if seenCap[capID] {
				t.Errorf("preset %q lists cap %q twice", p.ID, capID)
			}
			seenCap[capID] = true
		}
	}
}

// TestPresetsAdminExcludesServerDelete: the admin preset is "all but delete".
func TestPresetsAdminExcludesServerDelete(t *testing.T) {
	for _, p := range Presets() {
		if p.ID != "admin" {
			continue
		}
		for _, c := range p.Capabilities {
			if c == "server.delete" {
				t.Fatal("admin preset must not include server.delete")
			}
		}
	}
}
