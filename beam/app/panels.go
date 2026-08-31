package main

import (
	"fmt"
	"net/url"
	"strings"
)

// More than one panel, and switching between them without signing in again.
//
// Beam pointed at exactly one panel. That is right for a customer with one
// account and wrong for anyone who runs a production panel and a test one, or
// who self-hosts beside the hosted service: the only way across was to retype
// the URL, which also threw away the session.
//
// The list is stored beside the single value rather than replacing it, and the
// single value still MIGRATES into the list on read. Anyone on the current build
// has that field on disk; a schema that ignored it would open on the build
// default and look like the app had forgotten where it was pointed, which is the
// one thing a desktop client must not do on update.

// savedPanel is one panel the app knows about.
type savedPanel struct {
	// Name is what the user typed. Optional: an unnamed entry shows its host,
	// which is what people recognise anyway.
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
	// APIURL is the per-panel API origin override, empty for the same-origin
	// default. Per panel and not global: the whole point of the list is that the
	// entries are different deployments.
	APIURL string `json:"apiUrl,omitempty"`
}

// DisplayName is what a list shows for this entry.
func (p savedPanel) DisplayName() string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	if u, err := url.Parse(strings.TrimSpace(p.URL)); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimSpace(p.URL)
}

// OfficialPanelName is what the built-in entry is called in the list. Named
// rather than derived from its host, because "the official one" is the thing it
// is, and a customer who added a second panel needs to tell them apart at a
// glance.
const OfficialPanelName = "Dylaris Official"

// builtInPanelURL / builtInAPIURL are the EFFECTIVE launch defaults for this
// process: the DYLARIS_PANEL_URL / DYLARIS_API_URL environment, else the ldflags
// build values. main sets them once at startup.
//
// Package-level rather than read off the App, because panelList is a method on
// the stored settings and has no App to ask - and reading the raw build
// constants instead would ignore the environment override, which is how a
// self-hoster and every test point this app somewhere else.
var (
	builtInPanelURL = defaultPanelURL
	builtInAPIURL   = defaultAPIURL
)

// setBuiltInDefaults records the launch defaults. Called once from main, before
// any settings are read.
func setBuiltInDefaults(panelURL, apiURL string) {
	builtInPanelURL = panelURL
	builtInAPIURL = apiURL
}

// officialPanel is the built-in entry, or a zero value when this build has no
// panel compiled in (the open-source build).
func officialPanel() savedPanel {
	raw := strings.TrimSpace(builtInPanelURL)
	if raw == "" {
		return savedPanel{}
	}
	u, err := normalisePanelURL(raw)
	if err != nil {
		return savedPanel{}
	}
	return savedPanel{Name: OfficialPanelName, URL: u, APIURL: strings.TrimSpace(builtInAPIURL)}
}

// panelList returns the panels, migrating a pre-list config on the way, with the
// built-in entry always FIRST and always present.
//
// Always present because it is the one address this app is for: a user who
// removes it - or who edits its URL into something unreachable while testing -
// otherwise has a client that can no longer find the service it was installed to
// reach, and no way back except reinstalling. It is re-added rather than
// protected by the UI alone, so the guarantee holds for every path that writes
// the list, not only the button that has a confirm on it.
//
// A stored entry for the SAME host wins on its own settings: someone who added
// the official panel by hand before this existed keeps their name and API
// override rather than having them silently replaced.
//
// The legacy field is only consulted when the list is EMPTY. Reading both would
// resurrect a panel the user had removed, every launch.
func (s userSettings) panelList() []savedPanel {
	var stored []savedPanel
	switch {
	case len(s.Panels) > 0:
		for _, p := range s.Panels {
			if strings.TrimSpace(p.URL) == "" {
				continue
			}
			stored = append(stored, p)
		}
	case strings.TrimSpace(s.PanelURL) != "":
		stored = []savedPanel{{URL: strings.TrimSpace(s.PanelURL), APIURL: strings.TrimSpace(s.APIURL)}}
	}

	official := officialPanel()
	if official.URL == "" {
		return stored
	}
	out := make([]savedPanel, 0, len(stored)+1)
	out = append(out, official)
	for _, p := range stored {
		if sameHost(p.URL, official.URL) {
			// Their own row for it, kept as they configured it - but still first
			// and still under the official name, so the list reads the same on
			// every install.
			out[0] = savedPanel{Name: OfficialPanelName, URL: official.URL, APIURL: p.APIURL}
			continue
		}
		out = append(out, p)
	}
	return out
}

// IsOfficial reports whether this entry is the built-in one, which may not be
// removed or repointed.
func (p savedPanel) IsOfficial() bool {
	o := officialPanel()
	return o.URL != "" && sameHost(p.URL, o.URL)
}

// sameHost compares two panel URLs the way the session does - by host, since
// that is what a cookie jar is keyed on.
func sameHost(a, b string) bool {
	ua, erra := url.Parse(strings.TrimSpace(a))
	ub, errb := url.Parse(strings.TrimSpace(b))
	if erra != nil || errb != nil {
		return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	return strings.EqualFold(ua.Host, ub.Host)
}

// activePanel resolves which entry the window should be showing.
//
// An Active pointing at an entry that no longer exists falls back rather than to
// nothing: a removed panel must not leave the window proxying a URL that is not
// in the list, which is a state with no way out through the UI.
//
// The fallback is the first entry the USER configured, not simply the first
// entry - panelList puts the built-in one there, and taking it would repoint a
// self-hoster at the official panel the moment their stored choice went stale.
// Falling back to the built-in entry is the last resort, for an install that has
// configured nothing.
func (s userSettings) activePanel() savedPanel {
	list := s.panelList()
	if len(list) == 0 {
		return savedPanel{}
	}
	active := strings.TrimSpace(s.Active)
	for _, p := range list {
		if p.URL == active {
			return p
		}
	}
	for _, p := range list {
		if !p.IsOfficial() {
			return p
		}
	}
	return list[0]
}

// panelKey identifies a panel for anything held per panel - cookies, the
// readable-cookie replay. The HOST, because that is what a cookie is scoped to;
// two entries differing only in path are the same jar either way, and pretending
// otherwise would hand one of them the other's session.
func panelKey(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// normalisePanelURL is what a typed value becomes before it is stored.
func normalisePanelURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("a panel URL is required")
	}
	if !strings.HasPrefix(strings.ToLower(v), "http://") && !strings.HasPrefix(strings.ToLower(v), "https://") {
		v = "https://" + v
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not a URL this app can open", raw)
	}
	// Trailing slash off so the later concatenations ("/login", "/api") never
	// produce a double slash.
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}
