package main

import "testing"

func TestSplitWSFragments(t *testing.T) {
	cases := []struct {
		name  string
		size  int
		max   int
		wantN int
	}{
		{"empty", 0, 60, 1},
		{"under max", 10, 60, 1},
		{"exact max", 60, 60, 1},
		{"just over", 61, 60, 2},
		{"three fragments", 130, 60, 3},
		{"zero max falls back", 10, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := make([]byte, c.size)
			for i := range data {
				data[i] = byte(i)
			}
			frags := splitWSFragments(data, c.max)
			if len(frags) != c.wantN {
				t.Fatalf("splitWSFragments(size=%d,max=%d) => %d frags, want %d", c.size, c.max, len(frags), c.wantN)
			}
			var joined []byte
			for _, f := range frags {
				joined = append(joined, f...)
			}
			if len(joined) != c.size {
				t.Fatalf("reassembled len = %d, want %d", len(joined), c.size)
			}
			for i := range joined {
				if joined[i] != data[i] {
					t.Fatalf("reassembled[%d] = %d, want %d", i, joined[i], data[i])
				}
			}
			effMax := c.max
			if effMax <= 0 {
				effMax = wsFragmentSize
			}
			for i, f := range frags {
				if len(f) > effMax {
					t.Errorf("fragment %d len %d exceeds max %d", i, len(f), effMax)
				}
			}
		})
	}
}
