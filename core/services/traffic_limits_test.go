package services

import (
	"errors"
	"strconv"
	"testing"

	"dylaris-core/models"
)

func gbPtr(n int64) *int64 { return &n }

type fakeLimitReader struct {
	rows map[string]*models.TrafficLimit // "scope|region|kind" -> row
	err  error
	asks []string
}

func (f *fakeLimitReader) GetTrafficLimit(scope, region, kind string) (*models.TrafficLimit, error) {
	f.asks = append(f.asks, scope)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[scope+"|"+region+"|"+kind], nil
}

func TestResolveTrafficLimit_ScopeOrder(t *testing.T) {
	tests := []struct {
		name        string
		rows        map[string]*models.TrafficLimit
		wantScope   string
		wantIncl    *int64
		wantPurch   *int64
		wantAskedUp int // how many scopes should have been consulted
	}{
		{
			name:        "nothing configured anywhere is no limit",
			rows:        nil,
			wantScope:   "",
			wantIncl:    nil,
			wantPurch:   nil,
			wantAskedUp: 3,
		},
		{
			name: "global answers when nothing more specific does",
			rows: map[string]*models.TrafficLimit{
				"global|eu-central|edge": {IncludedGB: gbPtr(1000), MaxPurchaseGB: gbPtr(5000)},
			},
			wantScope:   "global",
			wantIncl:    gbPtr(1000),
			wantPurch:   gbPtr(5000),
			wantAskedUp: 3,
		},
		{
			name: "user_default beats global",
			rows: map[string]*models.TrafficLimit{
				"user_default|eu-central|edge": {IncludedGB: gbPtr(2000)},
				"global|eu-central|edge":       {IncludedGB: gbPtr(1000)},
			},
			wantScope:   "user_default",
			wantIncl:    gbPtr(2000),
			wantPurch:   nil,
			wantAskedUp: 2,
		},
		{
			name: "the user's own row beats both and stops the walk",
			rows: map[string]*models.TrafficLimit{
				"user:u1|eu-central|edge":      {IncludedGB: gbPtr(9000), MaxPurchaseGB: gbPtr(1)},
				"user_default|eu-central|edge": {IncludedGB: gbPtr(2000)},
				"global|eu-central|edge":       {IncludedGB: gbPtr(1000)},
			},
			wantScope:   "user:u1",
			wantIncl:    gbPtr(9000),
			wantPurch:   gbPtr(1),
			wantAskedUp: 1,
		},
		{
			// The distinction the whole convention exists for. A row that
			// exists has ANSWERED, and its answer may be NULL - "set, and no
			// limit". It must not fall through to a scope that would have said
			// something, or an operator lifting one tenant's cap would silently
			// hand them the global one instead.
			name: "a row with NULL values answers and does not fall through",
			rows: map[string]*models.TrafficLimit{
				"user:u1|eu-central|edge": {IncludedGB: nil, MaxPurchaseGB: nil},
				"global|eu-central|edge":  {IncludedGB: gbPtr(1000), MaxPurchaseGB: gbPtr(0)},
			},
			wantScope:   "user:u1",
			wantIncl:    nil,
			wantPurch:   nil,
			wantAskedUp: 1,
		},
		{
			// Zero is a real answer, not an absence. This is the case that
			// shipped as a defect four times on this platform before limits
			// became pointers.
			name: "zero is a cap, not a missing value",
			rows: map[string]*models.TrafficLimit{
				"global|ap-southeast|edge": {IncludedGB: gbPtr(0), MaxPurchaseGB: gbPtr(0)},
			},
			wantScope:   "global",
			wantIncl:    gbPtr(0),
			wantPurch:   gbPtr(0),
			wantAskedUp: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region := "eu-central"
			if _, ok := tt.rows["global|ap-southeast|edge"]; ok {
				region = "ap-southeast"
			}
			f := &fakeLimitReader{rows: tt.rows}
			got, err := ResolveTrafficLimit(f, "u1", region, "edge")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Scope != tt.wantScope {
				t.Errorf("scope = %q, want %q", got.Scope, tt.wantScope)
			}
			assertPtr(t, "includedGB", got.IncludedGB, tt.wantIncl)
			assertPtr(t, "maxPurchaseGB", got.MaxPurchaseGB, tt.wantPurch)
			if len(f.asks) != tt.wantAskedUp {
				t.Errorf("consulted %v, want %d scope(s)", f.asks, tt.wantAskedUp)
			}
		})
	}
}

// A (region, kind) is asked as a pair: a limit set for player traffic must not
// answer for file transfers, which are carried by a different component in a
// different region at a different price.
func TestResolveTrafficLimit_KindAndRegionAreThePair(t *testing.T) {
	f := &fakeLimitReader{rows: map[string]*models.TrafficLimit{
		"global|eu-central|edge": {IncludedGB: gbPtr(1000)},
	}}
	if got, _ := ResolveTrafficLimit(f, "u1", "eu-central", "relay"); got.Scope != "" {
		t.Errorf("an edge limit answered for relay: %+v", got)
	}
	if got, _ := ResolveTrafficLimit(f, "u1", "ap-southeast", "edge"); got.Scope != "" {
		t.Errorf("a eu-central limit answered for ap-southeast: %+v", got)
	}
}

func TestResolveTrafficLimit_StoreErrorSurfaces(t *testing.T) {
	f := &fakeLimitReader{err: errors.New("db down")}
	if _, err := ResolveTrafficLimit(f, "u1", "eu-central", "edge"); err == nil {
		t.Fatal("a store failure must not read as 'no limit configured'")
	}
}

func TestPurchasableGB(t *testing.T) {
	tests := []struct {
		name   string
		cap    *int64
		bought int64
		want   *int64
	}{
		{"no cap stays no cap", nil, 9999, nil},
		{"cap of zero sells nothing, and says 0 rather than nil", gbPtr(0), 0, gbPtr(0)},
		{"headroom is what is left", gbPtr(5000), 1200, gbPtr(3800)},
		{"exhausted is zero", gbPtr(5000), 5000, gbPtr(0)},
		{"over-bought never goes negative", gbPtr(5000), 6000, gbPtr(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PurchasableGB(ResolvedTrafficLimit{MaxPurchaseGB: tt.cap}, tt.bought)
			assertPtr(t, "purchasable", got, tt.want)
		})
	}
}

func assertPtr(t *testing.T, what string, got, want *int64) {
	t.Helper()
	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %s, want %s", what, ptrStr(got), ptrStr(want))
	case *got != *want:
		t.Errorf("%s = %d, want %d", what, *got, *want)
	}
}

func ptrStr(p *int64) string {
	if p == nil {
		return "nil"
	}
	return "ptr(" + strconv.FormatInt(*p, 10) + ")"
}
