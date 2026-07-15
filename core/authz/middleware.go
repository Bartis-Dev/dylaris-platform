package authz

import (
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// RequireCap wraps a handler so it only runs when the request principal holds
// capID. It is the enforcement half of the chokepoint; identity (AuthMiddleware)
// stays separate from authorization (this). Wiring it into the ~400 routes is
// phase 2; phase 1 only defines and unit-tests it.
//
//   - Unknown capID          -> 500 (developer misconfiguration, fail loud).
//   - No identity            -> 401.
//   - SERVER cap, no server  -> 403 (path lacks a resolvable {id}/{uuid}).
//   - Deny                   -> 403.
//   - Grant                  -> inner handler.
func (r *Resolver) RequireCap(capID string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			c, ok := Get(capID)
			if !ok {
				denyJSON(w, "unknown capability", http.StatusInternalServerError)
				return
			}
			id := IdentityFromContext(req.Context())
			if !id.IsAdmin && id.UserID == "" {
				denyJSON(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			serverID := 0
			if c.Scope == ScopeServer {
				serverID = r.serverIDFromRequest(req)
				if serverID == 0 {
					denyJSON(w, "Forbidden", http.StatusForbidden)
					return
				}
			}
			res, err := r.Resolve(id, serverID)
			if err != nil || !res.HasCap(capID) {
				denyJSON(w, "Forbidden", http.StatusForbidden)
				return
			}
			next(w, req)
		}
	}
}

// serverIDFromRequest reads the numeric server id from the {id} path var, or
// resolves the {uuid} path var to its numeric id. Returns 0 when neither is
// present or resolvable.
func (r *Resolver) serverIDFromRequest(req *http.Request) int {
	vars := mux.Vars(req)
	if idStr := vars["id"]; idStr != "" {
		if n, err := strconv.Atoi(idStr); err == nil {
			return n
		}
	}
	if uuid := vars["uuid"]; uuid != "" {
		if srv, err := r.store.GetServerByUUID(uuid); err == nil && srv != nil {
			return srv.ID
		}
	}
	return 0
}

func denyJSON(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
