package store

import "time"

// WarpAPIKey is an enrollment credential with a connection policy.
type WarpAPIKey struct {
	ID        int
	Name      string
	KeyHash   string
	Policy    string // "fixed" | "general"
	MaxConns  int
	OnNewConn string // "kill_old" | "block"
	FixedWGIP string // "" = auto-allocate
	NodeID    string
	RevokedAt *time.Time
	CreatedAt time.Time
}

// WarpPeer is one enrolled client: pubkey ↔ allocated WG IP.
type WarpPeer struct {
	ID        int
	APIKeyID  int
	Pubkey    string
	WGIP      string
	LeaderID  string
	CreatedAt time.Time
}
