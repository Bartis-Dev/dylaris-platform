package panelfs

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// NoncePlaceholder is what the build stamps onto every script tag, and what a
// fresh value replaces on the way out.
//
// CROSS-LANGUAGE CONTRACT with panel/scripts/stamp-nonce.mjs. If the two
// spellings ever diverge, every script ships un-nonced and the browser blocks
// the entire bundle - loud, immediate, and impossible to mistake for working.
const NoncePlaceholder = "__DYLARIS_CSP_NONCE__"

// headOpen is where the runtime-config script is injected.
//
// Injected by Core rather than declared in the panel's layout, for two reasons
// that both bit during the move:
//
//   - next/script's beforeInteractive inserts a <script src> from Next's CLIENT
//     runtime, and that runtime has no nonce to give it. A static export has no
//     request-time nonce for Next to embed, so the strict policy blocked it and
//     the panel silently fell back to a same-origin API URL.
//   - A placeholder declared in the React tree is ALSO serialised into Next's
//     flight payload, so it appears twice in the HTML: once as a tag, once
//     inside a JSON string. Filling both would put unescaped quotes into that
//     string and break hydration.
//
// Here it is a parser-inserted tag, nonced like every other one, and it runs
// before any bundle script because it is first.
const headOpen = "<head>"

// immutableAssets is the one prefix whose contents are content-addressed. Next
// puts a hash in every filename under it, so the bytes behind a given URL never
// change and the browser can be told to stop asking.
const immutableAssets = "_next/static/"

func (h *Handler) openFile(p string) (fs.File, bool) {
	if p == "" || strings.HasSuffix(p, "/") {
		return nil, false
	}
	f, err := h.files.Open(p)
	if err != nil {
		return nil, false
	}
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		f.Close()
		return nil, false
	}
	return f, true
}

// serveAsset answers a real file: a hashed chunk, a font, an icon.
func (h *Handler) serveAsset(w http.ResponseWriter, r *http.Request, p string, f fs.File) {
	if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if strings.HasPrefix(p, immutableAssets) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// Everything else is addressed by a stable name, so it has to be
		// revalidated or a panel update would not reach a browser that already
		// has it.
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	io.Copy(w, f)
}

// serveHTML answers a page: a fresh nonce, the matching policy, and no caching.
//
// no-store is not caution, it is required. The nonce in the body and the nonce
// in the Content-Security-Policy header have to be the same value, and a cache
// that holds one without the other - or serves the pair to a second visitor -
// produces a page whose own scripts the browser refuses. Pages are 18 KB and the
// chunks behind them are cached for a year, so the cost is one small document
// per navigation.
func (h *Handler) serveHTML(w http.ResponseWriter, r *http.Request, file string, found bool) {
	raw, err := fs.ReadFile(h.files, file)
	if err != nil {
		http.Error(w, "Panel bundle missing", http.StatusInternalServerError)
		return
	}
	nonce := newNonce()
	body := bytes.ReplaceAll(raw, []byte(NoncePlaceholder), []byte(nonce))
	body = injectConfig(body, nonce, h.configJS)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", h.csp.build(nonce))
	// The panel's own pages are never framed. Tenant content lives on its own
	// hosts and is dispatched before this handler is ever reached.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")

	status := http.StatusOK
	if !found {
		// A path the panel does not know is a real 404, not a silent redirect
		// to the dashboard: a mistyped or dead link should say so.
		status = http.StatusNotFound
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// newNonce is 16 bytes of randomness, base64 without padding.
//
// crypto/rand.Read cannot fail in the standard library's current
// implementation, and if it ever did the correct answer is not a predictable
// nonce - it is a panic, which is what Read already does internally on a broken
// entropy source.
func newNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("panelfs: no entropy for a CSP nonce: " + err.Error())
	}
	return base64.RawStdEncoding.EncodeToString(b[:])
}

// injectConfig puts the runtime-config script immediately after <head>.
//
// A page with no <head> is returned untouched rather than patched at a guessed
// position: the panel then resolves its API URL same-origin, which is the
// correct answer for every deployment that sets no PANEL_API_URL and a visible
// wrong one for the few that do. Guessing a position could produce a script
// outside <head> that runs after the bundle, which fails for everyone silently.
func injectConfig(html []byte, nonce string, config []byte) []byte {
	i := bytes.Index(html, []byte(headOpen))
	if i < 0 {
		return html
	}
	at := i + len(headOpen)
	tag := []byte(`<script nonce="` + nonce + `">`)
	out := make([]byte, 0, len(html)+len(tag)+len(config)+len("</script>"))
	out = append(out, html[:at]...)
	out = append(out, tag...)
	out = append(out, config...)
	out = append(out, "</script>"...)
	return append(out, html[at:]...)
}
