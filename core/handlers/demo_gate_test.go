package handlers

import (
	"database/sql"
	"errors"
	"testing"

	"dylaris-core/store"
)

type demoFakeStore struct {
	store.Store
	value string
	err   error
}

func (f *demoFakeStore) GetSetting(string) (string, error) { return f.value, f.err }

// The demo account's read-only gate in AuthMiddleware asks this. The lookup's
// error used to be discarded, and its zero value is "not the demo account" - so
// a failed settings read silently lifted the gate and a public demo session
// could mutate. Unknown has to be distinguishable from no.
func TestIsDemoAccountChecked(t *testing.T) {
	const demoID = "demo-uuid"

	tests := []struct {
		name      string
		state     *AppState
		userID    string
		wantDemo  bool
		wantError bool
	}{
		{
			name:     "the demo account, lookup succeeded",
			state:    &AppState{StoreEnabled: true, Store: &demoFakeStore{value: demoID}},
			userID:   demoID,
			wantDemo: true,
		},
		{
			name:     "an ordinary account, lookup succeeded",
			state:    &AppState{StoreEnabled: true, Store: &demoFakeStore{value: demoID}},
			userID:   "someone-else",
			wantDemo: false,
		},
		{
			name:     "no demo account configured",
			state:    &AppState{StoreEnabled: true, Store: &demoFakeStore{value: ""}},
			userID:   demoID,
			wantDemo: false,
		},
		{
			// The setting is never seeded, so this is what EVERY install looks
			// like until someone nominates a demo account. Reporting it as an
			// unknown made AuthMiddleware 503 every non-admin write on the whole
			// hosted panel.
			name:     "the setting row does not exist",
			state:    &AppState{StoreEnabled: true, Store: &demoFakeStore{err: sql.ErrNoRows}},
			userID:   demoID,
			wantDemo: false,
		},
		{
			// The case the discarded error created. It must NOT come back as a
			// plain false, which is what lifted the gate.
			name:      "lookup failed",
			state:     &AppState{StoreEnabled: true, Store: &demoFakeStore{err: errors.New("connection reset")}},
			userID:    demoID,
			wantError: true,
		},
		{
			// Not a SaaS build: there is no demo account and no lookup at all,
			// so this is a real "no", not an unknown.
			name:     "store integration disabled",
			state:    &AppState{StoreEnabled: false, Store: &demoFakeStore{err: errors.New("should not be called")}},
			userID:   demoID,
			wantDemo: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			demo, err := isDemoAccountChecked(tt.state, tt.userID)
			if (err != nil) != tt.wantError {
				t.Fatalf("err = %v, wantError = %v", err, tt.wantError)
			}
			if err == nil && demo != tt.wantDemo {
				t.Errorf("demo = %v, want %v", demo, tt.wantDemo)
			}
			if tt.wantError && demo {
				t.Error("an unknown answer must not report true either - that would block every non-admin")
			}
		})
	}
}

// The boolean wrapper stays for the render-only callers, and must keep behaving
// as before: unknown collapses to false there, where being wrong costs a
// misdrawn button rather than a lifted write gate.
func TestIsDemoAccount_WrapperCollapsesUnknownToFalse(t *testing.T) {
	s := &AppState{StoreEnabled: true, Store: &demoFakeStore{err: errors.New("boom")}}
	if isDemoAccount(s, "demo-uuid") {
		t.Error("wrapper reported true on a failed lookup")
	}
}
