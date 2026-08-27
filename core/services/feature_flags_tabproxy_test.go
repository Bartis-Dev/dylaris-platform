package services

import (
	"context"
	"errors"
	"testing"
)

type stubSettings struct{ m map[string]string }

func (s stubSettings) GetSetting(k string) (string, error) {
	v, ok := s.m[k]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestFeatureFlagsGetInt(t *testing.T) {
	ff := NewFeatureFlags(stubSettings{m: map[string]string{
		"good":  "7",
		"bad":   "notanumber",
		"blank": "",
	}})
	cases := []struct {
		key  string
		def  int
		want int
	}{
		{"good", 10, 7},
		{"missing", 10, 10},
		{"bad", 10, 10},
		{"blank", 10, 10},
	}
	for _, c := range cases {
		if got := ff.GetInt(context.Background(), c.key, c.def); got != c.want {
			t.Errorf("GetInt(%q, def=%d) = %d, want %d", c.key, c.def, got, c.want)
		}
	}
}

func TestTabProxyDefaults(t *testing.T) {
	ff := NewFeatureFlags(stubSettings{m: map[string]string{}})
	ctx := context.Background()
	if ff.IsTabProxyEnabled(ctx) {
		t.Error("IsTabProxyEnabled default = true, want false")
	}
	if ff.TabProxyAllowPublicLinks(ctx) {
		t.Error("TabProxyAllowPublicLinks default = true, want false")
	}
	if got := ff.TabProxyMaxPerUserPerServer(ctx); got != 3 {
		t.Errorf("TabProxyMaxPerUserPerServer default = %d, want 3", got)
	}
	if got := ff.TabProxyMaxPerUserTotal(ctx); got != 10 {
		t.Errorf("TabProxyMaxPerUserTotal default = %d, want 10", got)
	}
	if got := ff.TabProxyMaxShareLinksPerUser(ctx); got != 20 {
		t.Errorf("TabProxyMaxShareLinksPerUser default = %d, want 20", got)
	}
}

func TestTabProxyCapsFloorNonPositive(t *testing.T) {
	ff := NewFeatureFlags(stubSettings{m: map[string]string{
		"tab_proxy_max_per_user_per_server":  "0",
		"tab_proxy_max_per_user_total":       "-1",
		"tab_proxy_max_share_links_per_user": "-5",
	}})
	ctx := context.Background()
	if got := ff.TabProxyMaxPerUserPerServer(ctx); got != 3 {
		t.Errorf("TabProxyMaxPerUserPerServer(0) = %d, want floored 3", got)
	}
	if got := ff.TabProxyMaxPerUserTotal(ctx); got != 10 {
		t.Errorf("TabProxyMaxPerUserTotal(-1) = %d, want floored 10", got)
	}
	if got := ff.TabProxyMaxShareLinksPerUser(ctx); got != 20 {
		t.Errorf("TabProxyMaxShareLinksPerUser(-5) = %d, want floored 20", got)
	}
}
