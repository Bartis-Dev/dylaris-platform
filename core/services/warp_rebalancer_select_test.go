package services

import (
	"testing"

	"dylaris-pkg/protocol"
)

func peer(pk string, tx uint64) protocol.PeerBandwidth {
	return protocol.PeerBandwidth{Pubkey: pk, TxBps: tx}
}

func TestSelectWarpMoves(t *testing.T) {
	t.Run("moves largest peers first until shed is met", func(t *testing.T) {
		in := warpMoveInput{
			saturated: []saturatedLeader{{
				leaderID: "L-a", region: "eu-1", shedBps: 300,
				peers: []protocol.PeerBandwidth{peer("p1", 100), peer("p2", 250), peer("p3", 50)},
			}},
			targets:  []moveTarget{{leaderID: "L-b", region: "eu-1", headroomBps: 1000}},
			maxMoves: 10,
		}
		got := selectWarpMoves(in)
		// p2 (250) then p1 (100) = 350 >= 300; p3 not needed.
		if len(got) != 2 || got[0].Pubkey != "p2" || got[1].Pubkey != "p1" {
			t.Fatalf("got %+v, want [p2 p1]", got)
		}
		if got[0].To != "L-b" || got[0].From != "L-a" {
			t.Fatalf("wrong from/to: %+v", got[0])
		}
	})

	t.Run("not-sustained (empty saturated) does nothing", func(t *testing.T) {
		if got := selectWarpMoves(warpMoveInput{maxMoves: 10}); len(got) != 0 {
			t.Fatalf("got %+v, want none", got)
		}
	})

	t.Run("hysteresis: skip a target that cannot absorb the peer", func(t *testing.T) {
		in := warpMoveInput{
			saturated: []saturatedLeader{{
				leaderID: "L-a", region: "eu-1", shedBps: 300,
				peers: []protocol.PeerBandwidth{peer("p1", 250)},
			}},
			targets:  []moveTarget{{leaderID: "L-b", region: "eu-1", headroomBps: 100}}, // < 250
			maxMoves: 10,
		}
		if got := selectWarpMoves(in); len(got) != 0 {
			t.Fatalf("got %+v, want none (no target with headroom)", got)
		}
	})

	t.Run("hysteresis: never re-move a recently moved peer", func(t *testing.T) {
		in := warpMoveInput{
			saturated: []saturatedLeader{{
				leaderID: "L-a", region: "eu-1", shedBps: 200,
				peers: []protocol.PeerBandwidth{peer("p1", 250)},
			}},
			targets:       []moveTarget{{leaderID: "L-b", region: "eu-1", headroomBps: 1000}},
			recentlyMoved: map[string]bool{"p1": true},
			maxMoves:      10,
		}
		if got := selectWarpMoves(in); len(got) != 0 {
			t.Fatalf("got %+v, want none (p1 recently moved)", got)
		}
	})

	t.Run("move cap bounds the tick", func(t *testing.T) {
		in := warpMoveInput{
			saturated: []saturatedLeader{{
				leaderID: "L-a", region: "eu-1", shedBps: 1000,
				peers: []protocol.PeerBandwidth{peer("p1", 100), peer("p2", 100), peer("p3", 100)},
			}},
			targets:  []moveTarget{{leaderID: "L-b", region: "eu-1", headroomBps: 100000}},
			maxMoves: 2,
		}
		if got := selectWarpMoves(in); len(got) != 2 {
			t.Fatalf("got %d moves, want 2 (cap)", len(got))
		}
	})

	t.Run("only same-region targets are eligible", func(t *testing.T) {
		in := warpMoveInput{
			saturated: []saturatedLeader{{
				leaderID: "L-a", region: "eu-1", shedBps: 100,
				peers: []protocol.PeerBandwidth{peer("p1", 100)},
			}},
			targets:  []moveTarget{{leaderID: "L-x", region: "us-1", headroomBps: 100000}},
			maxMoves: 10,
		}
		if got := selectWarpMoves(in); len(got) != 0 {
			t.Fatalf("got %+v, want none (target in another region)", got)
		}
	})

	t.Run("decrements target headroom across moves in one tick", func(t *testing.T) {
		in := warpMoveInput{
			saturated: []saturatedLeader{{
				leaderID: "L-a", region: "eu-1", shedBps: 400,
				peers: []protocol.PeerBandwidth{peer("p1", 150), peer("p2", 150)},
			}},
			targets:  []moveTarget{{leaderID: "L-b", region: "eu-1", headroomBps: 200}}, // fits only one 150
			maxMoves: 10,
		}
		got := selectWarpMoves(in)
		if len(got) != 1 || got[0].Pubkey != "p1" {
			t.Fatalf("got %+v, want exactly [p1] (headroom exhausted after first)", got)
		}
	})
}
