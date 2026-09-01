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

// Where the button starts.
//
// It defaulted to the LEFT margin, which is where the panel keeps its sidebar:
// the button landed on top of the panel's own controls, hard to pick out and in
// the one corner where a stray click costs something. The default is the right
// edge now, and it stays there through a resize until the user drags it.
func TestLauncherDefaultsToTheBottomRight(t *testing.T) {
	got := launcherTag("", false)
	if !strings.Contains(got, "function defaultX()") {
		t.Fatal("no default position is computed at all")
	}
	if !strings.Contains(got, "window.innerWidth - SIZE - MARGIN") {
		t.Error("the default is not derived from the right edge")
	}
	if !strings.Contains(got, "place(stored() === null ? defaultX() : stored())") {
		t.Error("an unplaced button does not start at the default")
	}
	// The resize handler has to re-derive it. Clamping alone would leave a
	// never-moved button wherever the previous window size put it, so "bottom
	// right" would stop being true the moment the window changed.
	if !strings.Contains(got, "place(stored() === null ? defaultX() :") {
		t.Error("a resize does not re-anchor an unplaced button to the right edge")
	}
}

// The re-attach guard has to watch the node the button actually lives in.
//
// It observed documentElement with subtree:false, which fires only when <head>
// or <body> THEMSELVES are swapped - something React does not do. A removal from
// inside body, the only way this node can really disappear, reached the observer
// never, and the button stayed gone until a full page load.
func TestLauncherReattachWatchesTheSubtree(t *testing.T) {
	got := launcherTag("", false)
	if strings.Contains(got, "subtree: false") {
		t.Error("the observer still ignores everything inside body, where the button is")
	}
	if !strings.Contains(got, "observe(document.documentElement, { childList: true, subtree: true })") {
		t.Error("the re-attach observer does not cover the subtree")
	}
	// And it must still be idempotent: the callback runs on every mutation
	// batch of a busy SPA, so it may only touch the DOM when the host is gone.
	if !strings.Contains(got, "host.isConnected) return;") {
		t.Error("attach() would re-append on every mutation")
	}
}

// The re-attach must be BOUNDED.
//
// Watching the subtree means the append is itself an observed mutation. If
// anything in the page removes this node on sight, an unbounded re-attach feeds
// it - remove, observe, append, observe, remove - which pegs the renderer and
// takes the window white. The cap turns that back into a missing button, which
// is a bug rather than a hang.
func TestLauncherReattachIsBounded(t *testing.T) {
	got := launcherTag("", false)
	if !strings.Contains(got, "MAX_ATTACHES") {
		t.Fatal("the re-attach has no ceiling; a page that removes the node loops forever")
	}
	if !strings.Contains(got, "observer.disconnect()") {
		t.Error("hitting the ceiling does not stop observing, so the callback keeps running")
	}
	// The cheap guard has to come FIRST: the callback runs on every mutation
	// batch of a busy application, so anything before this early return is paid
	// thousands of times per page.
	if !strings.Contains(got, "if (!document.body || host.isConnected) return;") {
		t.Error("attach() does no early return, so it works on every mutation batch")
	}
}
