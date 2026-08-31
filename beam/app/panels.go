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

// panelList returns the panels, migrating a pre-list config on the way.
//
// The legacy field is only consulted when the list is EMPTY. Reading both would
// resurrect a panel the user had removed, every launch.
func (s userSettings) panelList() []savedPanel {
	if len(s.Panels) > 0 {
		out := make([]savedPanel, 0, len(s.Panels))
		for _, p := range s.Panels {
			if strings.TrimSpace(p.URL) == "" {
				continue
			}
			out = append(out, p)
		}
		return out
	}
	if strings.TrimSpace(s.PanelURL) == "" {
		return nil
	}
	return []savedPanel{{URL: strings.TrimSpace(s.PanelURL), APIURL: strings.TrimSpace(s.APIURL)}}
}

// activePanel resolves which entry the window should be showing.
//
// An Active pointing at an entry that no longer exists falls back to the first
// rather than to nothing: a removed panel must not leave the window proxying a
// URL that is not in the list, which is a state with no way out through the UI.
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
