package main

import (
	"os"
	"strings"
	"testing"
)

// The launcher runs inside somebody else's application, on the origin where
// window.go is bound. Three properties have to hold or it is either invisible or
// dangerous.
func TestLauncherTag(t *testing.T) {
	t.Run("it is stamped with the page's nonce", func(t *testing.T) {
		got := launcherTag("abc123", false)
		if !strings.HasPrefix(got, `<script nonce="abc123">`) {
			t.Errorf("un-nonced under a strict policy the browser drops it: %.60s", got)
		}
	})

	// A Core that sends no nonce falls back to a policy with unsafe-inline, and
	// a nonce attribute with an empty value would be worse than none: it opts
	// the tag into nonce checking that nothing can satisfy.
	t.Run("no nonce means no attribute", func(t *testing.T) {
		if got := launcherTag("", false); !strings.HasPrefix(got, "<script>") {
			t.Errorf("an empty nonce was stamped anyway: %.60s", got)
		}
	})

	t.Run("it opens the app-shell settings route", func(t *testing.T) {
		if got := launcherTag("", false); !strings.Contains(got, beamSettingsRoute) {
			t.Error("the button navigates nowhere")
		}
	})
}

// The stylesheet is spliced into a JavaScript string inside a <script> element.
// A quote would end the string and a "</style" or "</script" would end the
// element, so the encoding has to cover the tag and not only the quotes.
func TestLauncherStylesCannotEscapeTheScript(t *testing.T) {
	got := goStringLiteral(`a { content: "x" } </script> <b>`)
	for _, forbidden := range []string{`</script>`, `<b>`} {
		if strings.Contains(got, forbidden) {
			t.Errorf("%q survived into the literal: %s", forbidden, got)
		}
	}
	// No raw < may survive: that character is the only way out of the
	// element, whichever tag follows it.
	if strings.ContainsRune(got, '<') {
		t.Errorf("a raw less-than sign survived: %s", got)
	}
	if !strings.Contains(got, `\"x\"`) {
		t.Errorf("the quotes were not escaped: %s", got)
	}
	// And the real stylesheet has to survive the same treatment intact.
	if !strings.Contains(goStringLiteral(launcherStyles), "z-index") {
		t.Error("the stylesheet did not make it through the encoder")
	}
}

// Every proxied HTML response carries it, or the settings page is reachable
// only from the error screen again - which is the state this replaced.
func TestTheLauncherIsInjectedIntoProxiedPages(t *testing.T) {
	b, err := os.ReadFile("proxy.go")
	if err != nil {
		t.Fatal(err)
	}
	// Matched on the call, not its full argument list: the arguments legitimately
	// change (the update flag was added after this test), and pinning the exact
	// text makes the test fail for an edit that keeps the property it guards.
	if !strings.Contains(string(b), "launcherTag(nonce") {
		t.Error("nothing injects the launcher; Beam's settings are unreachable while the panel works")
	}
}

// The update dot is the only notice a user gets while the window is showing the
// panel, so it has to be stamped into the tag rather than fetched by the script -
// which runs inside somebody else's application and must not make calls.
func TestLauncherStampsTheUpdateFlag(t *testing.T) {
	if got := launcherTag("", true); !strings.Contains(got, "var hasUpdate = true") {
		t.Error("an available update was not stamped into the launcher")
	}
	if got := launcherTag("", false); !strings.Contains(got, "var hasUpdate = false") {
		t.Error("the flag must always be a literal, or the script has an undefined name in it")
	}
	// The placeholder must never survive: it is not valid JavaScript, so a
	// missed substitution takes the whole button out rather than degrading.
	if got := launcherTag("", true); strings.Contains(got, "__HAS_UPDATE__") {
		t.Error("the placeholder was left in the script")
	}
}
