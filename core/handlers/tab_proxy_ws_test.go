package handlers

import "testing"

func TestSplitProxyBytes(t *testing.T) {
	cases := []struct {
		name  string
		size  int
		max   int
		wantN int
	}{
		{"empty", 0, 60, 1},
		{"under", 30, 60, 1},
		{"exact", 60, 60, 1},
		{"over", 61, 60, 2},
		{"triple", 130, 60, 3},
		{"zero max falls back", 5, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := make([]byte, c.size)
			for i := range data {
				data[i] = byte(i)
			}
			frags := splitProxyBytes(data, c.max)
			if len(frags) != c.wantN {
				t.Fatalf("splitProxyBytes(size=%d,max=%d) => %d frags, want %d", c.size, c.max, len(frags), c.wantN)
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
		})
	}
}
