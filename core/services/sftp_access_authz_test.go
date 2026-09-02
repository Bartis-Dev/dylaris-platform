package services

import (
	"testing"

	"dylaris-core/authz"
	"dylaris-core/models"
	"dylaris-core/store"
)

// sftpAuthzFakeStore is the slice of persistence the resolver needs. Only the
// grant lookups vary between cases; everything else answers deny-by-default.
type sftpAuthzFakeStore struct {
	server *models.Server
	grant  *store.ServerGrant
	role   *store.ServerRole
}

func (f *sftpAuthzFakeStore) GetServerByID(int) (*models.Server, error)      { return f.server, nil }
func (f *sftpAuthzFakeStore) GetServerByUUID(string) (*models.Server, error) { return f.server, nil }
func (f *sftpAuthzFakeStore) GetPanelRole(int) (*store.PanelRole, error)     { return nil, nil }
func (f *sftpAuthzFakeStore) GetServerRole(int) (*store.ServerRole, error)   { return f.role, nil }
func (f *sftpAuthzFakeStore) GetUserPanelAuthz(string) (*int, store.CapOverrides, error) {
	return nil, store.CapOverrides{}, nil
}
func (f *sftpAuthzFakeStore) GetServerGrant(int, string) (*store.ServerGrant, error) {
	return f.grant, nil
}
func (f *sftpAuthzFakeStore) GetAccountGrant(string, string) (*store.ServerGrant, error) {
	return nil, nil
}

// What lands in the SFTP keys IS the access decision - the node has no second
// gate behind them. A member row used to be published on sight, so an invite
// with every capability off opened a full read/write session on someone else's
// server while the same account got 403 on every HTTP file route. This is that
// case, plus the ones the fix must not break.
func TestMayUseSFTP(t *testing.T) {
	const (
		ownerID  = "11111111-1111-4111-8111-111111111111"
		friendID = "22222222-2222-4222-8222-222222222222"
		serverID = 7
	)
	srv := &models.Server{ID: serverID, UUID: "srv-uuid", OwnerID: ownerID}

	tests := []struct {
		name    string
		row     store.SFTPAccess
		isAdmin bool
		grant   *store.ServerGrant
		role    *store.ServerRole
		want    bool
		why     string
	}{
		{
			name: "the owner",
			row:  store.SFTPAccess{ServerID: serverID, UserID: ownerID, Username: "owner", IsOwner: true},
			want: true,
			why:  "an owner short-circuits in the resolver too; refusing here would lock people out of their own servers",
		},
		{
			name:  "a member with no capabilities at all",
			row:   store.SFTPAccess{ServerID: serverID, UserID: friendID, Username: "bob"},
			grant: &store.ServerGrant{ServerID: &[]int{serverID}[0], UserID: friendID},
			want:  false,
			why:   "this is the bug: measured live, this account could read server.properties and delete server.jar over SFTP",
		},
		{
			name:  "a member granted sftp.access",
			row:   store.SFTPAccess{ServerID: serverID, UserID: friendID, Username: "bob"},
			grant: &store.ServerGrant{ServerID: &[]int{serverID}[0], UserID: friendID, CapOverrides: store.CapOverrides{Grant: []string{"sftp.access"}}},
			want:  true,
			why:   "the whole point is that the capability still works when it is actually granted",
		},
		{
			name:  "a member granted only console access",
			row:   store.SFTPAccess{ServerID: serverID, UserID: friendID, Username: "bob"},
			grant: &store.ServerGrant{ServerID: &[]int{serverID}[0], UserID: friendID, CapOverrides: store.CapOverrides{Grant: []string{"console.read", "console.send"}}},
			want:  false,
			why:   "console access is not file access; the two are separate capabilities on purpose",
		},
		{
			name:  "a member whose role carries sftp.access",
			row:   store.SFTPAccess{ServerID: serverID, UserID: friendID, Username: "bob"},
			grant: &store.ServerGrant{ServerID: &[]int{serverID}[0], UserID: friendID, ServerRoleID: &[]int{3}[0]},
			role:  &store.ServerRole{ID: 3, Capabilities: []string{"sftp.access"}},
			want:  true,
			why:   "roles are the normal way this is granted, not overrides",
		},
		{
			name:    "an admin who was invited",
			row:     store.SFTPAccess{ServerID: serverID, UserID: friendID, Username: "root"},
			isAdmin: true,
			grant:   &store.ServerGrant{ServerID: &[]int{serverID}[0], UserID: friendID},
			want:    true,
			why:     "an admin holds every capability; resolving them as an ordinary user would drop access they do have",
		},
		{
			name: "a member row with no grant behind it",
			row:  store.SFTPAccess{ServerID: serverID, UserID: friendID, Username: "ghost"},
			want: false,
			why:  "nothing grants it, so nothing may be published",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &sftpAuthzFakeStore{server: srv, grant: tt.grant, role: tt.role}
			s := &SFTPSyncService{authz: authz.NewResolver(fake)}
			if got, _ := s.mayUseSFTP(tt.row, tt.isAdmin); got != tt.want {
				t.Errorf("mayUseSFTP = %v, want %v: %s", got, tt.want, tt.why)
			}
		})
	}
}

// A service built without a resolver must publish nothing rather than
// everything. Nothing constructs one that way today, and that is exactly why
// the direction it fails in has to be pinned.
func TestMayUseSFTPWithoutAResolver(t *testing.T) {
	s := &SFTPSyncService{}
	if ok, _ := s.mayUseSFTP(store.SFTPAccess{ServerID: 1, UserID: "u", Username: "bob"}, false); ok {
		t.Error("a nil resolver published a member's SFTP access")
	}
	ok, perms := s.mayUseSFTP(store.SFTPAccess{ServerID: 1, UserID: "u", Username: "owner", IsOwner: true}, false)
	if !ok {
		t.Error("a nil resolver withheld an OWNER's access to their own server")
	}
	// And the owner short-circuit has to produce FULL permissions, not a zero
	// value: an owner locked out of their own files is the other way to get
	// this wrong, and it would look identical in the boolean above.
	if !perms.Read || !perms.Write || !perms.Delete {
		t.Errorf("an owner was published with %+v; the short-circuit must grant everything the resolver would", perms)
	}
}
