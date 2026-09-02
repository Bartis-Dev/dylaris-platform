package services

import (
	"testing"

	"dylaris-core/authz"
	"dylaris-core/models"
	"dylaris-core/store"
)

// The built-in Builder role is the case this whole change is about.
//
// Builder is defined as write-but-not-delete: it holds files.write and
// mods.write and deliberately neither files.delete nor mods.delete. The HTTP
// file API enforces that per operation. SFTP and beam asked one question -
// sftp.access - and then allowed everything, so an account invited as a Builder
// was refused a delete over HTTP and could remove server.jar over SFTP.
//
// This pins the PUBLISHED answer, which is what the node enforces. A role that
// changes shape without this list changing with it shows up here.
func TestPublishedFilePermsMatchTheRole(t *testing.T) {
	const serverID = 7
	const friendID = "friend-1"
	srv := &models.Server{ID: serverID, UUID: "srv", OwnerID: "owner-1"}

	tests := []struct {
		name                string
		role                *store.ServerRole
		wantOK              bool
		wantR, wantW, wantD bool
		why                 string
	}{
		{
			name: "builder writes but does not delete",
			role: &store.ServerRole{ID: 3, Capabilities: []string{
				"sftp.access", "files.read", "files.write",
			}},
			wantOK: true, wantR: true, wantW: true, wantD: false,
			why: "a Builder refused a delete over HTTP could remove server.jar over SFTP",
		},
		{
			name: "viewer reads only",
			role: &store.ServerRole{ID: 3, Capabilities: []string{
				"sftp.access", "files.read",
			}},
			wantOK: true, wantR: true, wantW: false, wantD: false,
			why: "the Viewer role is described as read-only access to files",
		},
		{
			name: "server admin holds all three",
			role: &store.ServerRole{ID: 3, Capabilities: []string{
				"sftp.access", "files.read", "files.write", "files.delete",
			}},
			wantOK: true, wantR: true, wantW: true, wantD: true,
			why: "the role that does hold delete must keep it",
		},
		{
			// sftp.access authorizes the TRANSPORT and still decides whether a
			// session exists at all - see TestMayUseSFTP, which pins that. The
			// session is published and can then do nothing, which is what the
			// permissions say rather than a second place deciding it.
			name:   "sftp.access with no file verb grants a session that may do nothing",
			role:   &store.ServerRole{ID: 3, Capabilities: []string{"sftp.access"}},
			wantOK: true, wantR: false, wantW: false, wantD: false,
			why: "the verbs decide what a session may do, not whether it exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &sftpAuthzFakeStore{server: srv, role: tt.role,
				grant: &store.ServerGrant{
					ServerID:     &[]int{serverID}[0],
					UserID:       friendID,
					ServerRoleID: &[]int{3}[0],
				}}
			s := &SFTPSyncService{authz: authz.NewResolver(fake)}

			ok, perms := s.mayUseSFTP(store.SFTPAccess{
				ServerID: serverID, UserID: friendID, Username: "friend",
			}, false)

			if ok != tt.wantOK {
				t.Fatalf("published = %v, want %v: %s", ok, tt.wantOK, tt.why)
			}
			if !ok {
				return
			}
			if perms.Read != tt.wantR || perms.Write != tt.wantW || perms.Delete != tt.wantD {
				t.Errorf("published %+v, want read=%v write=%v delete=%v: %s",
					perms, tt.wantR, tt.wantW, tt.wantD, tt.why)
			}
		})
	}
}
