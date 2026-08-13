package services

import (
	"sort"

	"dylaris-pkg/protocol"
)

// --- Pure selection (no I/O; fully table-tested) ---

type warpMove struct {
	Pubkey string `json:"pubkey"`
	From   string `json:"from"`
	To     string `json:"to"`
	TxBps  uint64 `json:"txBps"`
}

type saturatedLeader struct {
	leaderID string
	region   string
	shedBps  int64
	peers    []protocol.PeerBandwidth
}

type moveTarget struct {
	leaderID    string
	region      string
	headroomBps int64
}

type warpMoveInput struct {
	saturated     []saturatedLeader
	targets       []moveTarget
	recentlyMoved map[string]bool
	maxMoves      int
}

// selectWarpMoves picks the minimum set of peer moves that relieves each
// saturated leader onto the freest same-region sibling with headroom. Worst
// leaders (largest shed) are handled first; on each leader the largest peers
// move first (fewest moves). A move requires a same-region target whose
// remaining headroom covers the peer's Tx; that headroom is decremented as moves
// are committed so one target is never oversubscribed within a tick. Peers in
// recentlyMoved are skipped (anti-flap) and the total is capped at maxMoves.
// Pure: same input always yields the same output, no clock, no Redis.
func selectWarpMoves(in warpMoveInput) []warpMove {
	// Working copy of target headroom, keyed by leaderID.
	headroom := make(map[string]int64, len(in.targets))
	targetRegion := make(map[string]string, len(in.targets))
	targetIDs := make([]string, 0, len(in.targets))
	for _, t := range in.targets {
		headroom[t.leaderID] = t.headroomBps
		targetRegion[t.leaderID] = t.region
		targetIDs = append(targetIDs, t.leaderID)
	}

	sat := make([]saturatedLeader, len(in.saturated))
	copy(sat, in.saturated)
	sort.SliceStable(sat, func(i, j int) bool { return sat[i].shedBps > sat[j].shedBps })

	var moves []warpMove
	for _, sl := range sat {
		if in.maxMoves > 0 && len(moves) >= in.maxMoves {
			break
		}
		peers := make([]protocol.PeerBandwidth, len(sl.peers))
		copy(peers, sl.peers)
		sort.SliceStable(peers, func(i, j int) bool { return peers[i].TxBps > peers[j].TxBps })

		remaining := sl.shedBps
		for _, p := range peers {
			if remaining <= 0 {
				break
			}
			if in.maxMoves > 0 && len(moves) >= in.maxMoves {
				break
			}
			if in.recentlyMoved[p.Pubkey] {
				continue
			}
			// Best same-region target: most remaining headroom that still fits.
			best := ""
			var bestHeadroom int64 = -1
			for _, tid := range targetIDs {
				if tid == sl.leaderID || targetRegion[tid] != sl.region {
					continue
				}
				h := headroom[tid]
				if h >= int64(p.TxBps) && h > bestHeadroom {
					best = tid
					bestHeadroom = h
				}
			}
			if best == "" {
				continue // no target can absorb this peer; leave it
			}
			headroom[best] -= int64(p.TxBps)
			remaining -= int64(p.TxBps)
			moves = append(moves, warpMove{Pubkey: p.Pubkey, From: sl.leaderID, To: best, TxBps: p.TxBps})
		}
	}
	return moves
}
