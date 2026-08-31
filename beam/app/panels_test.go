package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The rule this decides: how the app remembers more than one panel, without
// losing the one setting an existing install already has.
//
// Beam held a single Panel URL. Anyone using it today has that value on disk, so
// a list that simply replaced it would open on the default panel and look like
// the app had forgotten where it was pointed - the one thing a desktop client
// must not do on update.
func TestPanelsMigrateFromTheSingleURL(t *testing.T) {
	// No built-in entry, so these describe the STORED list on its own. The
	// built-in one has its own test below.
	withBuiltInPanel(t, "", "")

	t.Run("an old config becomes a one-entry list", func(t *testing.T) {
		s := userSettings{PanelURL: "https://panel.example.com", APIURL: "https://api.example.com"}
		got := s.panelList()
		if len(got) != 1 {
			t.Fatalf("panelList() = %v, want one entry", got)
		}
		if got[0].URL != "https://panel.example.com" || got[0].APIURL != "https://api.example.com" {
			t.Errorf("the saved values were lost: %+v", got[0])
		}
	})

	t.Run("a config with a list ignores the legacy field", func(t *testing.T) {
		s := userSettings{
			PanelURL: "https://old.example.com",
			Panels:   []savedPanel{{Name: "Prod", URL: "https://a.example.com"}},
		}
		got := s.panelList()
		if len(got) != 1 || got[0].URL != "https://a.example.com" {
			t.Errorf("the legacy field leaked back in: %+v", got)
		}
	})

	// A fresh install has neither, and must not invent an entry: the build
	// default is resolved elsewhere and an empty list is what says "nothing has
	// been chosen here".
	t.Run("an empty config is an empty list", func(t *testing.T) {
		if got := (userSettings{}).panelList(); len(got) != 0 {
			t.Errorf("panelList() = %v, want empty", got)
		}
	})

	t.Run("a nameless entry falls back to its host", func(t *testing.T) {
		s := userSettings{Panels: []savedPanel{{URL: "https://panel.example.com/"}}}
		if got := s.panelList()[0].DisplayName(); got != "panel.example.com" {
			t.Errorf("DisplayName() = %q, want the host", got)
		}
	})
}

// The rule this decides: the panel this build is FOR is always in the list, at
// the top, and cannot be removed.
//
// Not a UI rule, because the UI is not the only writer. A list that could lose
// it leaves a client that cannot find the service it was installed to reach,
// with no way back except reinstalling - and "I was trying things out in
// settings" is exactly when that happens.
func TestTheBuiltInPanelIsAlwaysPresentAndFirst(t *testing.T) {
	withBuiltInPanel(t, "https://panel.dylaris.test", "")

	t.Run("it is prepended to whatever is stored", func(t *testing.T) {
		s := userSettings{Panels: []savedPanel{{Name: "Mine", URL: "https://mine.example.com"}}}
		got := s.panelList()
		if len(got) != 2 {
			t.Fatalf("panelList() = %+v, want the built-in entry plus the stored one", got)
		}
		if got[0].URL != "https://panel.dylaris.test" || got[0].Name != OfficialPanelName {
			t.Errorf("first entry = %+v, want the built-in one", got[0])
		}
		if got[1].Name != "Mine" {
			t.Errorf("the stored entry was lost: %+v", got[1])
		}
	})

	t.Run("removing it from storage does not remove it", func(t *testing.T) {
		s := userSettings{Panels: []savedPanel{{Name: "Mine", URL: "https://mine.example.com"}}}
		if !s.panelList()[0].IsOfficial() {
			t.Error("the built-in entry was not restored")
		}
	})

	// Someone who added it by hand before it was built in must not end up with
	// two rows for one host - they would share a cookie jar while looking like
	// separate places to be signed in.
	t.Run("a stored row for the same host is folded in, not duplicated", func(t *testing.T) {
		s := userSettings{Panels: []savedPanel{
			{Name: "whatever I called it", URL: "https://panel.dylaris.test", APIURL: "https://api.example.com"},
		}}
		got := s.panelList()
		if len(got) != 1 {
			t.Fatalf("panelList() = %+v, want one entry", got)
		}
		if got[0].APIURL != "https://api.example.com" {
			t.Errorf("their API override was discarded: %+v", got[0])
		}
	})

	// The one that would be a real regression: a self-hoster whose stored choice
	// goes stale must not be silently repointed at the official panel.
	t.Run("the fallback prefers what the user configured", func(t *testing.T) {
		s := userSettings{
			Panels: []savedPanel{{Name: "Mine", URL: "https://mine.example.com"}},
			Active: "https://gone.example.com",
		}
		if got := s.activePanel(); got.URL != "https://mine.example.com" {
			t.Errorf("activePanel() = %+v, want the user's own panel", got)
		}
	})

	t.Run("with nothing configured it is the active one", func(t *testing.T) {
		if got := (userSettings{}).activePanel(); got.URL != "https://panel.dylaris.test" {
			t.Errorf("activePanel() = %+v, want the built-in entry", got)
		}
	})
}

// The active panel has to survive a list that changed under it. Pointing at an
// entry that was removed would leave the window proxying nothing.
func TestActivePanelFallsBackToTheFirstEntry(t *testing.T) {
	withBuiltInPanel(t, "", "")
	s := userSettings{
		Panels: []savedPanel{{URL: "https://a.example.com"}, {URL: "https://b.example.com"}},
		Active: "https://gone.example.com",
	}
	if got := s.activePanel(); got.URL != "https://a.example.com" {
		t.Errorf("activePanel() = %+v, want the first entry", got)
	}

	s.Active = "https://b.example.com"
	if got := s.activePanel(); got.URL != "https://b.example.com" {
		t.Errorf("activePanel() = %+v, want the chosen entry", got)
	}
}

// The readable cookies are replayed into the page, and they must be replayed
// into the RIGHT page.
//
// One flat map keyed by cookie name was enough while there was one panel. With
// several, panel A's sign-in hint would be written into panel B's document -
// which tells B's login screen that a session exists when none does, and bounces
// the user around a panel they are not signed in to.
func TestReadableCookiesAreKeptPerPanel(t *testing.T) {
	a := &App{}
	first, _ := url.Parse("https://a.example.com/")
	second, _ := url.Parse("https://b.example.com/")

	resp := &http.Response{Header: http.Header{}}
	resp.Header.Add("Set-Cookie", "dylaris_signed_in=1; Path=/")
	a.rememberReadableCookies(first, resp)

	if got := a.readableCookieScript(first, ""); !strings.Contains(got, "dylaris_signed_in=1") {
		t.Errorf("the hint was not replayed into its own panel: %q", got)
	}
	if got := a.readableCookieScript(second, ""); got != "" {
		t.Errorf("another panel's cookie was replayed into this one: %q", got)
	}
}
