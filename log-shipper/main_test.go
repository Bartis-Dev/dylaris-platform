package main

import "testing"

func TestParseHeapAfterGC(t *testing.T) {
	cases := []struct {
		name string
		line string
		want int64
		ok   bool
	}{
		{
			name: "java17 g1 young pause MB",
			line: "[2026-05-25T13:01:51.000+0000][info][gc] GC(0) Pause Young (Normal) (G1 Evacuation Pause) 256M->50M(2048M) 12.345ms",
			want: 50,
			ok:   true,
		},
		{
			name: "java17 g1 mixed pause MB",
			line: "[2026-05-25T13:02:51.000+0000][info][gc] GC(12) Pause Young (Mixed) (G1 Preventive Collection) 1900M->1400M(2048M) 45.6ms",
			want: 1400,
			ok:   true,
		},
		{
			name: "java8 legacy KB",
			line: "[GC (Allocation Failure)  256000K->50000K(2048000K), 0.012345 secs]",
			want: 48, // 50000K / 1024 = 48 MB (rounded down)
			ok:   true,
		},
		{
			name: "non-gc info line",
			line: "[2026-05-25T13:01:51.000+0000][info][server] Done loading (5.2s)",
			want: 0,
			ok:   false,
		},
		{
			name: "minecraft chat line with arrow but no GC",
			line: "[Server thread/INFO]: <player> ->",
			want: 0,
			ok:   false,
		},
		{
			name: "after-zero is allowed (post-clear)",
			line: "[gc] GC(99) Pause Full 1M->0M(2048M) 200ms",
			want: 0,
			ok:   false, // we reject zero — would be misleading as a live metric
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseHeapAfterGC(c.line)
			if ok != c.ok {
				t.Fatalf("ok mismatch: got %v want %v (got=%d)", ok, c.ok, got)
			}
			if ok && got != c.want {
				t.Fatalf("value mismatch: got %d want %d", got, c.want)
			}
		})
	}
}
