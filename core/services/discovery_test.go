package services

import (
	"strings"
	"testing"
)

func TestDeriveLinkToken_Deterministic(t *testing.T) {
	t1 := DeriveLinkToken("node-abc", "secret-xyz")
	t2 := DeriveLinkToken("node-abc", "secret-xyz")
	if t1 != t2 {
		t.Fatalf("not deterministic: %s != %s", t1, t2)
	}
}

func TestDeriveLinkToken_IsHex64Chars(t *testing.T) {
	token := DeriveLinkToken("node-abc", "secret-xyz")
	if len(token) != 64 {
		t.Fatalf("expected 64-char hex, got length %d: %s", len(token), token)
	}
	for _, c := range token {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("non-hex character in token: %c", c)
		}
	}
}

func TestDeriveLinkToken_DifferentInputs(t *testing.T) {
	t1 := DeriveLinkToken("node-A", "secret")
	t2 := DeriveLinkToken("node-B", "secret")
	t3 := DeriveLinkToken("node-A", "other-secret")
	if t1 == t2 {
		t.Error("different nodeIDs must produce different tokens")
	}
	if t1 == t3 {
		t.Error("different secrets must produce different tokens")
	}
}

func TestSlicesEqual(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a", "b"}, []string{"a", "b"}, true},
		{[]string{"a", "b"}, []string{"a", "c"}, false},
		{[]string{"a"}, []string{"a", "b"}, false},
		{nil, nil, true},
		{[]string{}, []string{}, true},
		{[]string{"x"}, []string{"y"}, false},
	}
	for _, c := range cases {
		if got := slicesEqual(c.a, c.b); got != c.want {
			t.Errorf("slicesEqual(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
