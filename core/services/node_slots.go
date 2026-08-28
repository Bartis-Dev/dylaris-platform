package services

// NodeSlotStore is the narrow store surface NodeSlotsUsed needs.
type NodeSlotStore interface {
	CountNodesByOwner(ownerID string) (int, error)
	CountPendingNodeEnrollTokens(userID string) (int, error)
	CountNodeWarpKeysByOwner(ownerID string) (int, error)
}

// NodeSlotsUsed is how many node slots a tenant occupies against max_nodes.
//
// A tenant can reach a node by TWO self-service routes - an enroll token
// (POST /api/nodes/enroll-token) and a warp node key (POST /api/warp/node-keys)
// - and the two live in different tables. Every site that asks "how many nodes
// does this tenant have" has to count BOTH pending kinds alongside the live
// nodes, or the answers disagree with each other.
//
// They did. The enroll mint counted nodes + enroll tokens, the warp mint counted
// nodes + warp keys, and the over-limit sweep counted nodes + warp keys. Neither
// mint gate could see the other's pending identities, so a two-node tenant could
// hold two enroll tokens AND two warp keys. The over-provisioning was not the
// sharp end: the redeem-time check still capped live nodes, so the tenant
// redeemed two tokens and was left holding two warp keys they could no longer
// use - and the SWEEP counts those, put them at 4 against a cap of 2, and cut
// off everything they ran after the 72h window. They were punished for a state
// the mint gates had handed them.
//
// A pending identity counts because it is a machine mid-setup: that is the
// reason CountNodeWarpKeysByOwner was written, and it applies to an enroll token
// in exactly the same way. Both counts are already scoped to LIVE, unredeemed
// identities (unconsumed and unexpired tokens; unrevoked keys with no peer), so
// nothing stale is counted and a tenant cannot be wedged by an abandoned one.
//
// NOT used at redeem time. There the identity being redeemed is still pending,
// so it would count against itself and the last slot could never be claimed;
// that check stays on live nodes alone and is the backstop behind these gates.
func NodeSlotsUsed(st NodeSlotStore, userID string) (int64, error) {
	nodes, err := st.CountNodesByOwner(userID)
	if err != nil {
		return 0, err
	}
	tokens, err := st.CountPendingNodeEnrollTokens(userID)
	if err != nil {
		return 0, err
	}
	keys, err := st.CountNodeWarpKeysByOwner(userID)
	if err != nil {
		return 0, err
	}
	return int64(nodes + tokens + keys), nil
}
