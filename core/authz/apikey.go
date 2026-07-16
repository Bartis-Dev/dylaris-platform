package authz

// ValidKeyCap reports whether capID may be carried by an API key: it must be a
// known catalog capability whose scope is SERVER or OWNER. PANEL caps (staff
// operations) and unknown ids are rejected - a key is an owner-scoped automation
// credential, not a staff principal.
func ValidKeyCap(capID string) bool {
	c, ok := Get(capID)
	return ok && (c.Scope == ScopeServer || c.Scope == ScopeOwner)
}

// ResolveAPIKey builds a Resolution representing an API key's authority: the
// key's SERVER caps (only when serverAllowed, i.e. the key's server scope covers
// the addressed server) plus its OWNER caps. It has no admin/owner short-
// circuit and no panel caps - a key holds exactly the capabilities it was
// minted with. Routing key checks through this + HasCap unifies key auth with
// session auth on the one decision function.
func ResolveAPIKey(perms []string, serverAllowed bool) *Resolution {
	res := &Resolution{
		panelCaps:  map[string]bool{},
		serverCaps: map[string]bool{},
		ownerCaps:  map[string]bool{},
	}
	for _, capID := range perms {
		c, ok := Get(capID)
		if !ok {
			continue
		}
		switch c.Scope {
		case ScopeServer:
			if serverAllowed {
				res.serverCaps[capID] = true
			}
		case ScopeOwner:
			res.ownerCaps[capID] = true
		}
	}
	return res
}
