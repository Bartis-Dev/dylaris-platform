package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"dylaris-core/storage/modpack"
)

// modpackPresignTTL bounds a handed-out download URL.
//
// Five minutes, matching the library download on the same codebase. An earlier
// value of six hours was justified as "long enough for a slow client to finish
// a multi-GB pack", which misreads what the TTL does: the signature is checked
// when the request is RECEIVED, so it bounds when a download may START, not how
// long it may take. An in-flight transfer is unaffected by the URL expiring.
//
// The short window matters most on the share route. That link used to be
// streamed through Core, so revoking it cut access at once. A presigned URL is
// a bearer credential the object store honours with no reference to Core, so
// whatever TTL is set here is the window in which a revoked - or expired -
// share link still serves the pack to anyone the URL was forwarded to.
const modpackPresignTTL = 5 * time.Minute

// SolderMirrorRequestsPerMinute is the per-IP budget on the public mirror
// route. See the route registration in routes.go for why it sits this high:
// the budget counts requests and a single pack install fetches one per mod, so
// a tight limit would 429 a legitimate install part way through.
const SolderMirrorRequestsPerMinute = 600

// modpackDelivery selects how a stored object reaches the client.
type modpackDelivery int

const (
	// deliverStream always copies the bytes through Core. Correct for clients
	// whose redirect behaviour is not ours to assume.
	deliverStream modpackDelivery = iota
	// deliverRedirect hands the client a presigned URL when the backend can
	// produce one, and streams otherwise. Core leaves the data path entirely
	// in the redirect case.
	deliverRedirect
)

// serveModpackObject writes a stored modpack object to the response.
//
// It exists to keep whole packs out of Core's heap. Serving them through
// ModpackStorageProvider.Get materialised the entire object per request, so a
// fleet installing one pack across N nodes had Core holding N copies of it at
// once - and the public mirror route that does this carries no authentication.
// Streaming makes the cost a fixed buffer regardless of pack size or
// concurrency.
//
// Callers map the returned error themselves: the two call sites answer in
// different error shapes (Solder-flavoured JSON vs the panel's), and
// modpack.ErrNotFound has to become a 404 rather than a 500 in both.
func serveModpackObject(w http.ResponseWriter, r *http.Request, prov modpack.ModpackStorageProvider, key string, mode modpackDelivery, contentType, filename string) error {
	if mode == deliverRedirect {
		url, err := prov.DownloadURL(r.Context(), key, modpackPresignTTL)
		switch {
		case err != nil:
			// Not fatal. Streaming is the correct answer too, just a more
			// expensive one, so the request is served rather than failed - but
			// it is logged, because a backend that should be able to presign
			// and cannot is a misconfiguration worth seeing.
			log.Printf("modpack: could not presign %s, streaming instead: %v", key, err)
		case url != "":
			http.Redirect(w, r, url, http.StatusFound)
			return nil
		}
		// An empty URL with no error is the documented "this backend cannot
		// presign" answer, not a failure. Fall through and stream.
	}

	rc, size, err := prov.Stream(r.Context(), key)
	if err != nil {
		return err
	}
	defer rc.Close()

	w.Header().Set("Content-Type", contentType)
	if filename != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	}
	if size != modpack.SizeUnknown {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	// Past this point the status line is already committed, so a failure
	// mid-body cannot be reported to the client as an error code. The client
	// sees a short read - which, with Content-Length set, it can detect. All
	// this side can do is record it.
	if _, cerr := io.Copy(w, rc); cerr != nil {
		log.Printf("modpack: streaming %s to the client failed part way through: %v", key, cerr)
	}
	return nil
}
