package services

import (
	"reflect"
	"testing"
)

func TestSolderBaseCandidates(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"bare host adds /api", "http://h:28080", []string{"http://h:28080", "http://h:28080/api"}},
		{"trailing slash trimmed then /api", "http://h:28080/", []string{"http://h:28080", "http://h:28080/api"}},
		{"already /api is not doubled", "http://h:28080/api", []string{"http://h:28080/api"}},
		{"already /api with trailing slash", "http://h:28080/api/", []string{"http://h:28080/api"}},
		{"whitespace trimmed", "  http://h  ", []string{"http://h", "http://h/api"}},
		{"empty stays empty", "", []string{""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := solderBaseCandidates(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("solderBaseCandidates(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestIsSolderInfoBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"technicsolder info", `{"api":"TechnicSolder","version":"1.0.0","stream":"stable"}`, true},
		{"solderpy info", `{"api":"solder.py","version":"v1.7.4"}`, true},
		{"empty api rejected", `{"api":""}`, false},
		{"no api field rejected", `{"modpacks":{},"mirror_url":"http://x/"}`, false},
		{"html rejected", `<!doctype html><html></html>`, false},
		{"garbage rejected", `not json`, false},
		{"empty body rejected", ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isSolderInfoBody([]byte(c.body)); got != c.want {
				t.Errorf("isSolderInfoBody(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}
