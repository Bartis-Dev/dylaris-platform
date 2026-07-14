package main

import "testing"

func TestConnectionModeRoundTrip(t *testing.T) {
	a := NewApp()
	if got := a.GetConnectionMode(); got != "" {
		t.Fatalf("fresh App connMode = %q, want \"\"", got)
	}
	a.setConnMode("lan-fastpath")
	if got := a.GetConnectionMode(); got != "lan-fastpath" {
		t.Errorf("after setConnMode: GetConnectionMode() = %q, want \"lan-fastpath\"", got)
	}
}

func TestConnectionModeResetPaths(t *testing.T) {
	tests := []struct {
		name  string
		reset func(a *App)
	}{
		{name: "resetSession clears mode (Logout path)", reset: func(a *App) { a.resetSession() }},
		{name: "newClientResetRelay clears mode (SetSession path)", reset: func(a *App) { a.newClientResetRelay(nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewApp()
			a.setConnMode("relay")
			tt.reset(a)
			if got := a.GetConnectionMode(); got != "" {
				t.Errorf("after reset: GetConnectionMode() = %q, want \"\"", got)
			}
		})
	}
}
