package dispatcher

import (
	"errors"
	"net/url"

	"github.com/wailsapp/wails/v2/internal/frontend"
)

// processBrowserMessage processing browser messages
func (d *Dispatcher) processBrowserMessage(message string, sender frontend.Frontend) (string, error) {
	if len(message) < 2 {
		return "", errors.New("Invalid Browser Message: " + message)
	}
	switch message[1] {
	case 'O':
		rawURL := message[3:]
		// BC3 hard boundary: 'O' (BrowserOpenURL) is the ONLY path a
		// JS-originated open travels - window.runtime.BrowserOpenURL and a
		// raw WailsInvoke("BO:"+url) both post "BO:"+url, which lands here.
		// The beam webview reverse-proxies an untrusted remote Panel onto
		// its own origin, so this dispatcher treats the URL as
		// attacker-controlled and hands only web schemes to the native OS
		// shell-open. Everything else (file://, UNC, bare/drive paths,
		// javascript:, empty, unparseable) is dropped. Go-origin callers
		// such as RevealInExplorer call runtime.BrowserOpenURL directly and
		// never reach this dispatcher, so they are intentionally unaffected.
		// net/url.Parse lowercases the scheme, so uppercase web schemes
		// still pass while everything non-web fails closed.
		if u, err := url.Parse(rawURL); err == nil {
			switch u.Scheme {
			case "http", "https", "mailto":
				go sender.BrowserOpenURL(rawURL)
			default:
				d.log.Warning("blocked BrowserOpenURL for non-web scheme: %s", rawURL)
			}
		} else {
			d.log.Warning("blocked BrowserOpenURL, unparseable URL: %s", rawURL)
		}
	default:
		d.log.Error("unknown Browser message: %s", message)
	}

	return "", nil
}
