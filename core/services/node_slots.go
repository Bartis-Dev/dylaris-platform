package services

// NodeSlotStore is the narrow store surface the node accounting needs.
type NodeSlotStore interface {
	CountNodesByOwner(ownerID string) (int, error)
	CountPendingNodeEnrollTokens(userID string) (int, error)
	CountNodeWarpKeysByOwner(ownerID string) (int, error)
}

// NodeSlots is what a tenant holds against max_nodes, kept as its three parts
// because a mint gate has to know which part it is about to add to.
//
// The unit of the cap is a MACHINE, and a BYON machine is two credentials: a
// warp key to reach the overlay and an enroll token to become a node. The panel
// mints both, back to back, for one machine the user named once.
//
// Counting them as two occupants is what this replaced, and it made the cap
// unusable rather than merely wrong: on max_nodes = 1 - which is what a manual
// grant and a one-unit purchase both resolve to - the warp key filled the
// allowance and the enroll token two seconds later was refused with "Node limit
// reached (1)". The FIRST machine could never be added, and revoking and
// retrying reproduced it exactly.
//
// The sweep saw the same double count from the other side: a tenant who did get
// both halves out sat at 2 against a cap of 1 and was cut off after 72 hours for
// a state the mint gates had handed them.
type NodeSlots struct {
	// Nodes are machines that have enrolled.
	Nodes int
	// Tokens and Keys are the pending halves. Both counts are already scoped to
	// LIVE, unredeemed identities (unconsumed and unexpired tokens; unrevoked
	// keys with no peer), so a machine that finishes setup leaves neither behind
	// and an abandoned attempt ages out on its own.
	Tokens int
	Keys   int
}

// Used is how many machines the tenant occupies.
//
// The pending halves are paired by taking the LARGER of the two rather than
// their sum: a machine mid-setup shows up in both counts, so summing bills it
// twice. The pairing is positional because nothing links a token row to a key
// row, which under-counts only when the two halves belong to DIFFERENT machines
// - two half-built machines, neither of which can enroll, since one has no
// overlay key and the other no way to become a node. The redeem-time check caps
// live nodes regardless, so that state costs a slot in the accounting and
// nothing in reality.
func (s NodeSlots) Used() int64 {
	pending := s.Tokens
	if s.Keys > pending {
		pending = s.Keys
	}
	return int64(s.Nodes + pending)
}

// UsedWithEnrollToken and UsedWithWarpKey report what the tenant would occupy
// after minting that half.
//
// A mint gate asks it this way round because minting is not always a new
// machine: the second half of a machine already counted adds nothing, and a gate
// that asked "are you at your cap" before the mint could not tell the two apart.
// That is the whole reason these exist rather than an AtOrOver on Used.
func (s NodeSlots) UsedWithEnrollToken() int64 { s.Tokens++; return s.Used() }
func (s NodeSlots) UsedWithWarpKey() int64     { s.Keys++; return s.Used() }

// CountNodeSlots reads the three counters. An error from any of them travels:
// a counter that silently reported 0 would open the cap wide exactly when the
// database is unhappy.
func CountNodeSlots(st NodeSlotStore, userID string) (NodeSlots, error) {
	var s NodeSlots
	var err error
	if s.Nodes, err = st.CountNodesByOwner(userID); err != nil {
		return NodeSlots{}, err
	}
	if s.Tokens, err = st.CountPendingNodeEnrollTokens(userID); err != nil {
		return NodeSlots{}, err
	}
	if s.Keys, err = st.CountNodeWarpKeysByOwner(userID); err != nil {
		return NodeSlots{}, err
	}
	return s, nil
}

// NodeSlotsUsed is the count for a reader that is not minting anything - the
// over-limit sweep and the panel's own "used of limit".
//
// NOT used at redeem time. There the identity being redeemed is still pending,
// so it would count against itself and the last slot could never be claimed;
// that check stays on live nodes alone and is the backstop behind these gates.
func NodeSlotsUsed(st NodeSlotStore, userID string) (int64, error) {
	s, err := CountNodeSlots(st, userID)
	if err != nil {
		return 0, err
	}
	return s.Used(), nil
}
