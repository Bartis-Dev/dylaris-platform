package main

import "strings"

// The way back into Beam's own settings.
//
// While the panel is reachable the window IS the panel: Beam has no chrome, no
// menu and nothing of its own on screen. The only route to the settings page was
// the "cannot reach the panel" error, so the app's configuration was reachable
// exactly when the app was broken - and the panel list this now opens would have
// been unreachable for anyone whose panel works.
//
// So the shell injects one control into the proxied document. Three things keep
// it from being a nuisance:
//
//   - A shadow root, so no panel stylesheet can reach it and it cannot reach the
//     panel's. The panel is a whole application; a floating div sharing its CSS
//     would eventually collide with something.
//   - Draggable along the bottom edge and remembered per install, because the
//     panel has its own controls down there and where they sit is not something
//     this can know.
//   - It opens the settings page as a NAVIGATION. Rendering Beam's settings
//     inside the panel's document would put them on the panel's origin with the
//     panel's scripts running beside them, and the shell token lives there.
//
// It is deliberately not a "Beam status bar". A fixed strip costs a row of the
// panel on every screen, forever, to show something that matters twice a month.

// launcherStyles is kept apart only so the markup below stays readable. It is
// inlined into the shadow root, which is why it needs no scoping of its own.
const launcherStyles = `
:host { all: initial; }
.dot {
  position: fixed;
  z-index: 2147483000;
  width: 34px; height: 34px;
  display: flex; align-items: center; justify-content: center;
  border-radius: 10px;
  background: #12141A;
  border: 1px solid #2A2F38;
  color: #8A95A8;
  cursor: grab;
  box-shadow: 0 2px 10px rgba(0,0,0,.45);
  transition: color .15s ease, border-color .15s ease, background .15s ease;
  user-select: none; -webkit-user-select: none;
  font: 500 12px/1 system-ui, -apple-system, sans-serif;
}
.dot:hover { color: #D8DDE6; border-color: #3A414D; background: #171A21; }
.dot:focus-visible { outline: 2px solid #7048C8; outline-offset: 2px; }
.dot.dragging { cursor: grabbing; opacity: .85; }
/* An update is waiting. A dot on the button rather than anything that covers the
   panel: this is a "when you get a moment" signal, and the one update that
   cannot wait has its own blocking screen. */
.dot.has-update::after {
  content: '';
  position: absolute;
  top: -2px; right: -2px;
  width: 9px; height: 9px;
  border-radius: 50%;
  background: #7048C8;
  border: 2px solid #12141A;
}
.dot svg { width: 17px; height: 17px; display: block; }
@media (prefers-reduced-motion: reduce) { .dot { transition: none; } }
`

// launcherScript is the injected control.
//
// Plain DOM, no framework, no fetch: it runs inside somebody else's application
// and the only thing it is allowed to depend on is the platform.
const launcherScript = `
(function () {
  if (window.__beamLauncher) return;
  window.__beamLauncher = true;

  var KEY = 'beam.launcher.x';
  var host = document.createElement('div');
  host.setAttribute('data-beam-launcher', '');
  var root = host.attachShadow({ mode: 'closed' });

  var style = document.createElement('style');
  style.textContent = __STYLES__;
  root.appendChild(style);

  var btn = document.createElement('button');
  btn.className = 'dot';
  btn.type = 'button';
  var hasUpdate = __HAS_UPDATE__;
  btn.title = hasUpdate ? 'Dylaris Beam settings - an update is available' : 'Dylaris Beam settings';
  btn.setAttribute('aria-label', btn.title);
  if (hasUpdate) btn.classList.add('has-update');
  btn.innerHTML = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>';
  root.appendChild(btn);

  // Bottom edge only, and clamped on every read rather than only on drop: a
  // window narrowed since the last session would otherwise restore the button
  // off-screen, where nothing can bring it back.
  var MARGIN = 12, SIZE = 34;
  function clamp(x) {
    var max = Math.max(MARGIN, window.innerWidth - SIZE - MARGIN);
    return Math.min(max, Math.max(MARGIN, x));
  }
  function stored() {
    try { var v = parseInt(localStorage.getItem(KEY) || '', 10); return isNaN(v) ? null : v; } catch (e) { return null; }
  }
  // Bottom RIGHT until the user moves it. The left corner is where the panel
  // keeps its sidebar - so the default position put this on top of the panel's
  // own controls, which is both hard to see and the one place a stray click is
  // expensive.
  function defaultX() { return Math.max(MARGIN, window.innerWidth - SIZE - MARGIN); }
  function place(x) {
    btn.style.left = clamp(x) + 'px';
    btn.style.bottom = MARGIN + 'px';
  }
  place(stored() === null ? defaultX() : stored());
  // An untouched button stays anchored to the right edge as the window changes
  // size; a placed one only gets clamped back into view. Re-deriving the
  // default here is what keeps "bottom right" true after a resize.
  window.addEventListener('resize', function () {
    place(stored() === null ? defaultX() : (parseInt(btn.style.left, 10) || defaultX()));
  });

  // A drag must not also be a click. The threshold is what separates "moved it"
  // from "pressed it", and without it every reposition would also open settings.
  var dragging = false, moved = false, offset = 0;
  btn.addEventListener('pointerdown', function (e) {
    dragging = true; moved = false;
    offset = e.clientX - btn.getBoundingClientRect().left;
    btn.classList.add('dragging');
    btn.setPointerCapture(e.pointerId);
  });
  btn.addEventListener('pointermove', function (e) {
    if (!dragging) return;
    if (Math.abs(e.clientX - (btn.getBoundingClientRect().left + offset)) > 3) moved = true;
    place(e.clientX - offset);
  });
  btn.addEventListener('pointerup', function (e) {
    if (!dragging) return;
    dragging = false;
    btn.classList.remove('dragging');
    btn.releasePointerCapture(e.pointerId);
    try { localStorage.setItem(KEY, String(parseInt(btn.style.left, 10) || MARGIN)); } catch (err) { /* private mode */ }
  });
  btn.addEventListener('click', function () { if (!moved) window.location.href = '__SETTINGS__'; });

  // Re-attach rather than assuming one insertion holds for the life of the
  // document: this button lives inside somebody else's single-page app, and
  // anything that rebuilds a subtree can take it with it.
  //
  // The subtree matters. This used to watch documentElement with subtree:false,
  // which fires only when <head> or <body> THEMSELVES are swapped - something
  // React does not do. A removal from INSIDE body, which is the only way this
  // node can actually disappear, reached the observer never, so the button
  // stayed gone until a full page load.
  //
  // Bounded, and that bound is load-bearing rather than tidiness. Watching the
  // subtree means an append is itself an observed mutation, so if anything in
  // the page removes this node on sight, re-attaching would feed it: remove,
  // observe, append, observe, remove - a loop that pegs the renderer and takes
  // the whole window white. A cap turns that worst case back into the old
  // behaviour, a missing button, which is a bug and not a hang.
  var attempts = 0, MAX_ATTACHES = 20;
  var observer;
  function attach() {
    if (!document.body || host.isConnected) return;
    if (++attempts > MAX_ATTACHES) { if (observer) observer.disconnect(); return; }
    document.body.appendChild(host);
  }
  attach();
  observer = new MutationObserver(attach);
  observer.observe(document.documentElement, { childList: true, subtree: true });
})();
`

// launcherTag returns the injected script element, nonce-stamped so a
// nonce-strict policy admits it.
//
// hasUpdate is stamped in rather than fetched by the script: the launcher runs
// inside somebody else's application and must not make calls of its own, and the
// value is re-stamped on every proxied page anyway - so it refreshes as the user
// navigates, which is often enough for a notice that is not urgent.
func launcherTag(nonce string, hasUpdate bool) string {
	body := strings.ReplaceAll(launcherScript, "__STYLES__", goStringLiteral(launcherStyles))
	body = strings.ReplaceAll(body, "__SETTINGS__", beamSettingsRoute)
	flag := "false"
	if hasUpdate {
		flag = "true"
	}
	body = strings.ReplaceAll(body, "__HAS_UPDATE__", flag)
	attr := ""
	if nonce != "" {
		attr = ` nonce="` + nonce + `"`
	}
	return "<script" + attr + ">" + body + "</script>"
}

// goStringLiteral renders a string as a JavaScript literal.
//
// The stylesheet is ours and contains no quotes today, which is exactly why this
// exists: relying on that is how a later edit adding a quoted content rule
// breaks every page the shell serves.
func goStringLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '<':
			// Escaped so a rule can never close the script element it lives in.
			b.WriteString("\\u003c")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
