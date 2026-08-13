package services

import (
	"context"
	"testing"
)

// newFeatureFlagsForTest builds a FeatureFlags backed by the stubSettings fake
// store (defined in feature_flags_tabproxy_test.go), the same fake used by the
// other feature_flags tests in this package.
func newFeatureFlagsForTest(settings map[string]string) *FeatureFlags {
	return NewFeatureFlags(stubSettings{m: settings})
}

func TestWarpRebalanceSettings(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		settings map[string]string
		mode     string
		pct      int
		sustain  int
		interval int
	}{
		{"defaults when unset", map[string]string{}, "off", 80, 10, 5},
		{"valid values", map[string]string{
			"warp_rebalance_mode":         "armed",
			"warp_rebalance_pct":          "90",
			"warp_rebalance_sustain_min":  "15",
			"warp_rebalance_interval_min": "3",
		}, "armed", 90, 15, 3},
		{"out-of-range pct clamps to default, non-positive floors", map[string]string{
			"warp_rebalance_pct":          "5",
			"warp_rebalance_sustain_min":  "0",
			"warp_rebalance_interval_min": "-1",
		}, "off", 80, 10, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFeatureFlagsForTest(tt.settings) // helper: FeatureFlags backed by a fake store
			if got := f.WarpRebalanceMode(ctx); got != tt.mode {
				t.Errorf("mode = %q, want %q", got, tt.mode)
			}
			if got := f.WarpRebalancePct(ctx); got != tt.pct {
				t.Errorf("pct = %d, want %d", got, tt.pct)
			}
			if got := f.WarpRebalanceSustainMin(ctx); got != tt.sustain {
				t.Errorf("sustain = %d, want %d", got, tt.sustain)
			}
			if got := f.WarpRebalanceIntervalMin(ctx); got != tt.interval {
				t.Errorf("interval = %d, want %d", got, tt.interval)
			}
		})
	}
}
