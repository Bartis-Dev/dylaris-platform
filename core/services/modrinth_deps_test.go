package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// "Modrinth does not know this hash" and "Modrinth did not answer" are two
// different answers and only one of them is safe to show a person.
//
// The lookup used to collapse every failure into a nil, so an outage told every
// operator their jars were not on Modrinth - advice they might act on by
// deleting them. The status is now carried so the caller can tell the two apart.
func TestModrinthNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"a 404 is Modrinth answering that it does not know it", &ModrinthHTTPError{Status: 404}, true},
		{"a 503 is Modrinth being unwell", &ModrinthHTTPError{Status: 503}, false},
		{"a 429 is a rate limit, not an unknown hash", &ModrinthHTTPError{Status: 429}, false},
		{"a 500 is not an unknown hash", &ModrinthHTTPError{Status: 500}, false},
		{"a wrapped 404 is still a 404", fmt.Errorf("looking up sodium: %w", &ModrinthHTTPError{Status: 404}), true},
		{"a network error is not an answer at all", errors.New("dial tcp: i/o timeout"), false},
		{"no error is not a not-found", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModrinthNotFound(tt.err); got != tt.want {
				t.Errorf("ModrinthNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestModrinthHTTPErrorMessageCarriesTheStatus(t *testing.T) {
	err := &ModrinthHTTPError{Path: "/version_file/abc", Status: 503, Body: "upstream"}
	msg := err.Error()
	for _, want := range []string{"/version_file/abc", "503", "upstream"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not mention %q", msg, want)
		}
	}
}
