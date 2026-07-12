# Vendored dependencies

## `third_party/wails/v2` - vendored + patched Wails v2.10.1

`go.mod` pins Wails to the local tree via
`replace github.com/wailsapp/wails/v2 => ./third_party/wails/v2`.

This is upstream Wails v2.10.1 with ONE security patch (the "BC3" fix): the native
dispatcher's `processBrowserMessage`
(`third_party/wails/v2/internal/frontend/dispatcher/dispatcher.go`) enforces a
scheme allowlist on the `BrowserOpenURL` bridge call. Upstream Wails only guarded
`window.runtime.BrowserOpenURL` in JavaScript, which page script could bypass with
`window.WailsInvoke("BO:<url>")` to reach `RevealInExplorer` / shell-open with an
attacker-controlled string. Since the beam window reverse-proxies the untrusted
remote Panel at the same origin as the full native bridge, that JS-only guard was
not a real boundary. The patch moves enforcement into the native dispatcher, the
only seam page script cannot reach.

Consequences:
- The tree is committed (not a module-cache fetch) because the patch is not upstream.
- It must NOT be silently bumped to a stock Wails release - a plain `go get -u` would
  drop the patch and reopen the RCE class. To move to a newer Wails, re-apply the
  dispatcher patch on top and re-vendor.
- It is built via `wails build` with `GOWORK` honoring this module's own `replace`
  (the module is a member of the repo `go.work`, and module-level replaces are
  applied for workspace members).
