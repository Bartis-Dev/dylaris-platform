package agent

// NOTE ON SCOPE
//
// The task these tests were written for described a "raw packet parser" that
// decodes untrusted network bytes (ethertype / IP / TCP headers, length/offset
// fields). No such parser exists in this package: nothing here reads a []byte
// off the wire. The DDoS agent operates entirely on OS-level counters returned
// by gopsutil (net.IOCounters / net.Connections) and on already-computed
// PacketStats, so there is no offset/length decode that could do an
// out-of-bounds read.
//
// The genuine untrusted-input surface in this byte-identical code is the
// persisted JSON data file that loadData() reads and unmarshals, plus the
// security-critical pure numeric logic (subClamp underflow guard, the DDoS
// threshold state machine, and GetSmartHistory downsampling). These tests
// exercise those surfaces with the same hostile-input matrix the task asked
// for: empty, truncated, oversized, wrong-type and garbage input must be
// handled gracefully and must never panic.

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildJSONArray returns a JSON array literal of `elem` repeated `n` times.
func buildJSONArray(elem string, n int) string {
	if n <= 0 {
		return "[]"
	}
	return "[" + strings.TrimRight(strings.Repeat(elem+",", n), ",") + "]"
}

// subClamp is the underflow guard that stops a counter reset (raw unsigned
// wrap) from being reported as a near-2^64 traffic spike. It is small but
// security-relevant, so it gets exhaustive boundary coverage.
func TestSubClamp(t *testing.T) {
	tests := []struct {
		name string
		a, b uint64
		want uint64
	}{
		{"normal difference", 10, 3, 7},
		{"equal operands", 5, 5, 0},
		{"underflow clamps to zero", 3, 10, 0},
		{"zero minus max clamps", 0, math.MaxUint64, 0},
		{"max minus zero", math.MaxUint64, 0, math.MaxUint64},
		{"max minus one", math.MaxUint64, 1, math.MaxUint64 - 1},
		{"one minus max clamps", 1, math.MaxUint64, 0},
		{"both zero", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subClamp(tt.a, tt.b); got != tt.want {
				t.Fatalf("subClamp(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestDDoSDetectorEvaluateLevel checks the threshold classification at and
// around each boundary. Well-formed and boundary "packets" (PacketStats) must
// classify exactly as configured.
func TestDDoSDetectorEvaluateLevel(t *testing.T) {
	// DefaultThresholds: PpsWarning 50000, PpsCritical 200000,
	//                    CpsWarning 1000,  CpsCritical 5000.
	tests := []struct {
		name  string
		stats PacketStats
		want  AlertLevel
	}{
		{"all zero -> normal", PacketStats{}, AlertNormal},
		{"pps just below warning -> normal", PacketStats{PpsIn: 49999}, AlertNormal},
		{"pps at warning -> warning", PacketStats{PpsIn: 50000}, AlertWarning},
		{"pps split reaches warning -> warning", PacketStats{PpsIn: 30000, PpsOut: 20000}, AlertWarning},
		{"pps just below critical -> warning", PacketStats{PpsIn: 199999}, AlertWarning},
		{"pps at critical -> critical", PacketStats{PpsIn: 200000}, AlertCritical},
		{"cps just below warning -> normal", PacketStats{CpsIn: 999}, AlertNormal},
		{"cps at warning -> warning", PacketStats{CpsIn: 1000}, AlertWarning},
		{"cps at critical -> critical", PacketStats{CpsIn: 5000}, AlertCritical},
		{"cps critical with zero pps -> critical", PacketStats{CpsIn: 6000}, AlertCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDDoSDetector(filepath.Join(t.TempDir(), "ddos.json"))
			d.Evaluate(tt.stats)
			if got := d.GetStatus().Level; got != tt.want {
				t.Fatalf("Evaluate(%+v) level = %q, want %q", tt.stats, got, tt.want)
			}
		})
	}
}

// TestDDoSDetectorEvaluateExtremeNoPanic feeds hostile/extreme counters. The
// detector must never panic regardless of input magnitude.
func TestDDoSDetectorEvaluateExtremeNoPanic(t *testing.T) {
	d := NewDDoSDetector(filepath.Join(t.TempDir(), "ddos.json"))
	hostile := []PacketStats{
		{PpsIn: math.MaxUint64, PpsOut: math.MaxUint64, CpsIn: math.MaxUint64},
		{PpsIn: math.MaxUint64},
		{PpsOut: math.MaxUint64},
		{CpsIn: math.MaxUint64},
		{}, // return to normal, exercises the attack-end/save path
	}
	for _, s := range hostile {
		d.Evaluate(s) // must not panic
	}
	_ = d.GetStatus()
}

// TestDDoSDetectorPpsSumOverflowDocumented pins the CURRENT behavior of the
// PpsIn+PpsOut addition: it wraps on uint64 overflow rather than saturating.
// In practice pps derives from realistic counters and never approaches 2^64,
// so this is an observation, not an exploitable hole. The test documents the
// behavior so a future switch to saturating math is caught deliberately.
func TestDDoSDetectorPpsSumOverflowDocumented(t *testing.T) {
	d := NewDDoSDetector(filepath.Join(t.TempDir(), "ddos.json"))
	// MaxUint64 + 2 wraps to 1, which is below every threshold.
	d.Evaluate(PacketStats{PpsIn: math.MaxUint64, PpsOut: 2})
	if got := d.GetStatus().Level; got != AlertNormal {
		t.Fatalf("wrapped pps sum: level = %q, want %q (documents overflow-wrap)", got, AlertNormal)
	}
}

// TestDDoSDetectorAttackLifecycle drives the full state machine:
// normal -> warning (attack starts) -> critical (peak climbs) -> normal
// (attack recorded).
func TestDDoSDetectorAttackLifecycle(t *testing.T) {
	d := NewDDoSDetector(filepath.Join(t.TempDir(), "ddos.json"))

	d.Evaluate(PacketStats{PpsIn: 60000}) // enter warning
	if st := d.GetStatus(); st.Level != AlertWarning || st.StartedAt == nil {
		t.Fatalf("after warning: level=%q StartedAt=%v, want warning with StartedAt set", st.Level, st.StartedAt)
	}

	d.Evaluate(PacketStats{PpsIn: 250000}) // escalate, peak climbs
	if st := d.GetStatus(); st.Level != AlertCritical {
		t.Fatalf("after escalation: level=%q, want critical", st.Level)
	}

	d.Evaluate(PacketStats{}) // attack ends, gets recorded
	st := d.GetStatus()
	if st.Level != AlertNormal {
		t.Fatalf("after end: level=%q, want normal", st.Level)
	}
	if len(st.RecentAttacks) != 1 {
		t.Fatalf("recorded attacks = %d, want 1", len(st.RecentAttacks))
	}
	if st.RecentAttacks[0].PeakPps != 250000 {
		t.Fatalf("recorded peak = %d, want 250000", st.RecentAttacks[0].PeakPps)
	}
}

// TestDDoSDetectorLoadMalformedFile exercises the real untrusted-input surface:
// NewDDoSDetector -> loadData reads and JSON-parses a persisted data file that
// may be empty, truncated, oversized, wrong-typed or pure garbage. It must
// never panic; malformed input must fall back to safe defaults, and the
// detector must stay usable afterwards.
func TestDDoSDetectorLoadMalformedFile(t *testing.T) {
	def := DefaultThresholds()
	hugeAttacks := `{"recent_attacks":` +
		buildJSONArray(`{"started_at":1,"ended_at":2,"peak_pps":3,"duration_sec":4}`, 20000) + `}`

	tests := []struct {
		name    string
		content string
		want    DDoSThresholds // expected thresholds after load
	}{
		{"empty file", "", def},
		{"garbage bytes", "\x00\x01\x02 not json \xff\xfe", def},
		{"truncated json", `{"thresholds":{"pps_warning":`, def},
		{"empty json object", `{}`, def},
		{"json null", `null`, def},
		{"wrong value types", `{"thresholds":{"pps_warning":"lots"}}`, def},
		{"zero thresholds ignored", `{"thresholds":{"pps_warning":0,"pps_critical":0}}`, def},
		{"valid thresholds loaded", `{"thresholds":{"pps_warning":1,"pps_critical":2,"cps_warning":3,"cps_critical":4}}`,
			DDoSThresholds{PpsWarning: 1, PpsCritical: 2, CpsWarning: 3, CpsCritical: 4}},
		{"oversized recent_attacks", hugeAttacks, def},
		{"array where object expected", `[1,2,3]`, def},
		{"deeply nested garbage", strings.Repeat(`{"thresholds":`, 500), def},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ddos.json")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write temp file: %v", err)
			}
			d := NewDDoSDetector(path) // must not panic on hostile file bytes
			if got := d.GetThresholds(); got != tt.want {
				t.Fatalf("thresholds after load = %+v, want %+v", got, tt.want)
			}
			// Detector must remain usable after loading a hostile file.
			d.Evaluate(PacketStats{PpsIn: 1})
			_ = d.GetStatus()
		})
	}
}

// TestPacketCollectorLoadMalformedFile exercises the PacketCollector's persisted
// data file through the same hostile-input matrix, hermetically (no host
// gopsutil call). loadData must never panic, and GetSmartHistory must stay safe
// regardless of what did or did not load.
func TestPacketCollectorLoadMalformedFile(t *testing.T) {
	hugeHistory := `{"history":` +
		buildJSONArray(`{"ts":1,"pps_in":1,"pps_out":1,"cps":1,"active_conns":1}`, 20000) + `}`

	tests := []struct {
		name    string
		content string
		write   bool // false => file does not exist
	}{
		{"missing file", "", false},
		{"empty file", "", true},
		{"garbage bytes", "\xff\xfe\x00 nope", true},
		{"truncated json", `{"history":[{"ts":123`, true},
		{"empty json object", `{}`, true},
		{"json null", `null`, true},
		{"history wrong type", `{"history":"not an array"}`, true},
		{"oversized history", hugeHistory, true},
		{"array where object expected", `[1,2,3]`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "pkt.json")
			if tt.write {
				if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
					t.Fatalf("write temp file: %v", err)
				}
			}
			pc := &PacketCollector{dataFile: path}
			pc.loadData()             // must not panic on hostile file bytes
			_ = pc.GetSmartHistory(3600) // must stay safe after a hostile load
		})
	}
}

// TestPacketCollectorGetSmartHistory covers the downsampling path with
// adversarial history: empty, all-filtered-out, under the cap, over the cap
// (forces integer-division downsampling), and extreme values that overflow the
// running sums. None may panic.
func TestPacketCollectorGetSmartHistory(t *testing.T) {
	now := time.Now().Unix()
	mk := func(n int, val uint64) []PacketHistoryPoint {
		pts := make([]PacketHistoryPoint, n)
		for i := range pts {
			pts[i] = PacketHistoryPoint{
				Timestamp:   now,
				PpsIn:       val,
				PpsOut:      val,
				Cps:         val,
				ActiveConns: int(val & 0x7fffffff),
			}
		}
		return pts
	}

	tests := []struct {
		name         string
		history      []PacketHistoryPoint
		rangeSec     int64
		wantNonEmpty bool
		maxLen       int
	}{
		{"empty history", nil, 3600, false, 0},
		{"all older than cutoff", []PacketHistoryPoint{{Timestamp: now - 100000}}, 10, false, 0},
		{"under cap returned as-is", mk(50, 1), 1 << 40, true, 50},
		{"exactly at cap", mk(300, 1), 1 << 40, true, 300},
		{"over cap downsampled", mk(1000, 1), 1 << 40, true, 1000},
		{"extreme values no overflow panic", mk(1000, math.MaxUint64), 1 << 40, true, 1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc := &PacketCollector{}
			pc.data.History = tt.history
			got := pc.GetSmartHistory(tt.rangeSec)
			if tt.wantNonEmpty && len(got) == 0 {
				t.Fatalf("got empty result, want non-empty")
			}
			if !tt.wantNonEmpty && len(got) != 0 {
				t.Fatalf("got %d points, want empty", len(got))
			}
			if tt.maxLen > 0 && len(got) > tt.maxLen {
				t.Fatalf("result len %d exceeds input/cap %d", len(got), tt.maxLen)
			}
		})
	}
}
