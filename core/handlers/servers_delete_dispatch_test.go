package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dylaris-core/models"
	"dylaris-core/services"
	"dylaris-core/store"

	"github.com/gorilla/mux"
)

// deleteDispatchFakeStore drops the node lookup, which is the reachable version
// of this: an operator removes a dead node from the panel and then cleans up the
// servers that pointed at it.
type deleteDispatchFakeStore struct {
	store.Store
	deleted     bool
	nodeMissing bool
}

func (f *deleteDispatchFakeStore) GetServerByID(id int) (*models.Server, error) {
	return &models.Server{ID: id, UUID: "srv-uuid", OwnerID: "alice", NodeID: 3}, nil
}

func (f *deleteDispatchFakeStore) GetNodeByID(id int) (*models.Node, error) {
	if f.nodeMissing {
		return nil, errors.New("no such node")
	}
	return &models.Node{ID: id, Token: "node-token"}, nil
}

func (f *deleteDispatchFakeStore) DeleteServer(id int) error {
	f.deleted = true
	return nil
}

// Admin so the can_delete_servers gate is not what refuses.
func (f *deleteDispatchFakeStore) GetUserByID(id string) (*models.User, error) {
	return &models.User{ID: id, IsAdmin: true, Role: "admin"}, nil
}

func (f *deleteDispatchFakeStore) GetUserRegionIDs(id string) ([]string, error) {
	return nil, nil
}

func deleteServerRequest() *http.Request {
	req := httptest.NewRequest(http.MethodDelete, "/api/servers/7", nil)
	ctx := context.WithValue(req.Context(), "userID", "alice")
	ctx = context.WithValue(ctx, "isAdmin", true)
	return mux.SetURLVars(req.WithContext(ctx), map[string]string{"id": "7"})
}

// Deleting the DB row is what makes a server disappear from the panel; the
// queued "delete" command is the only thing that removes the container and its
// files. When the dispatch cannot happen the row still goes - refusing would
// make a server unremovable exactly when its node is permanently gone - so the
// container, its host port, its RAM and its disk are left behind, findable only
// through the orphan-adoption flow.
//
// That is a defensible trade. Making it silently was not: there was no log line
// and the response was a bare {"success":true}.
func TestDeleteServerWarnsWhenTheNodeWasNeverTold(t *testing.T) {
	fs := &deleteDispatchFakeStore{nodeMissing: true}
	h := &ServerHandler{state: &AppState{Store: fs, Events: services.NewSystemEventsPublisher(nil)}}
	rw := httptest.NewRecorder()

	h.DeleteServer(rw, deleteServerRequest())

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 - a missing node must not block the delete (body: %s)", rw.Code, rw.Body.String())
	}
	if !fs.deleted {
		t.Error("the server row was not deleted; an unreachable node must not make a server unremovable")
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rw.Body.String())
	}
	if body["success"] != true {
		t.Errorf("success = %v, want true", body["success"])
	}
	warn, _ := body["warning"].(string)
	if warn == "" {
		t.Error("no warning: the container and files are still on the node and nothing said so")
	}
}

// The ordinary path must stay quiet - a warning on every delete would train
// operators to ignore the one that matters.
func TestDeleteServerIsSilentWhenTheDispatchWorked(t *testing.T) {
	fs := &deleteDispatchFakeStore{}
	// Queue nil is one of the warning conditions, so this case needs a real one;
	// without a Redis to back it, assert the node-present half instead: the
	// warning must name the queue, not the node.
	h := &ServerHandler{state: &AppState{Store: fs, Events: services.NewSystemEventsPublisher(nil)}}
	rw := httptest.NewRecorder()

	h.DeleteServer(rw, deleteServerRequest())

	var body map[string]interface{}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rw.Body.String())
	}
	warn, _ := body["warning"].(string)
	if warn == "" {
		t.Fatal("expected a warning with no queue configured")
	}
	if !strings.Contains(warn, "queue") {
		t.Errorf("warning = %q; with the node present and the queue absent it must name the queue, "+
			"otherwise the operator is sent to look at the wrong thing", warn)
	}
}

// subServerDeleteFakeStore is the delete-dispatch fake with a settable
// can_delete_servers flag, so the capability gate can be exercised on its own.
type subServerDeleteFakeStore struct {
	deleteDispatchFakeStore
	admin      bool
	canDelete  bool
	statusSet  string
	activeSet  bool
	desiredSet bool
}

func (f *subServerDeleteFakeStore) GetUserByID(id string) (*models.User, error) {
	role := "user"
	if f.admin {
		role = "admin"
	}
	return &models.User{ID: id, IsAdmin: f.admin, Role: role, CanDeleteServers: f.canDelete}, nil
}

func (f *subServerDeleteFakeStore) GetServerByID(id int) (*models.Server, error) {
	return &models.Server{ID: id, UUID: "srv-uuid", OwnerID: "alice", NodeID: 3, ActiveSubServer: "survival"}, nil
}

// Audit is off for this server, which is the common case and keeps these
// cases about the capability gate rather than the audit trail.
func (f *subServerDeleteFakeStore) GetServerAuditState(serverID int) (bool, bool, int, error) {
	return false, false, 0, nil
}

func (f *subServerDeleteFakeStore) UpdateServerStatus(id int, status string) error {
	f.statusSet = status
	return nil
}
func (f *subServerDeleteFakeStore) UpdateServerActiveSubServer(id int, name string) error {
	f.activeSet = true
	return nil
}
func (f *subServerDeleteFakeStore) UpdateServerDesiredState(id int, state string) error {
	f.desiredSet = true
	return nil
}

// TestDeleteSubServerHonorsTheDeleteCapability pins that the account-level
// can_delete_servers flag guards BOTH delete handlers in this file.
//
// Both routes carry RequireCap("server.delete"); DeleteServer additionally
// checks the flag, with a comment saying "both gates must be open". Its sibling
// stopped at the route cap. A sub-server is a whole instance directory on the
// node, so an operator who deliberately withheld the flag still let the user
// destroy exactly the data it exists to protect - a server made of sub-servers
// could be emptied one slot at a time.
func TestDeleteSubServerHonorsTheDeleteCapability(t *testing.T) {
	tests := []struct {
		name       string
		admin      bool
		canDelete  bool
		wantStatus int
	}{
		{name: "no flag, not admin: refused like the full delete is", wantStatus: http.StatusForbidden},
		{name: "flag granted", canDelete: true, wantStatus: http.StatusOK},
		{name: "admin always passes", admin: true, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := &subServerDeleteFakeStore{admin: tt.admin, canDelete: tt.canDelete}
			h := &ServerHandler{state: &AppState{Store: fs, Events: services.NewSystemEventsPublisher(nil)}}

			req := httptest.NewRequest(http.MethodDelete, "/api/servers/7/sub-servers/survival", nil)
			ctx := context.WithValue(req.Context(), "userID", "alice")
			ctx = context.WithValue(ctx, "isAdmin", tt.admin)
			req = mux.SetURLVars(req.WithContext(ctx), map[string]string{"id": "7", "subServerName": "survival"})

			rw := httptest.NewRecorder()
			h.DeleteSubServer(rw, req)

			if rw.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rw.Code, tt.wantStatus, rw.Body.String())
			}
			if tt.wantStatus == http.StatusForbidden {
				if !strings.Contains(rw.Body.String(), "elevated permissions") {
					t.Errorf("body = %q, want the same refusal wording the full delete uses", rw.Body.String())
				}
				// A refusal must not have touched the row on the way out.
				if fs.statusSet != "" || fs.activeSet || fs.desiredSet {
					t.Error("the refused request still reset the server's state")
				}
			}
		})
	}
}
