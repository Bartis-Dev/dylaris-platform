package handlers

import (
	"testing"

	"dylaris-core/models"
	"dylaris-core/store"
)

// solderVisibilityStore answers only the lookups the Solder read path makes.
// The embedded nil store.Store panics on anything else rather than returning a
// zero value that quietly changes the answer.
type solderVisibilityStore struct {
	store.Store

	public     []models.Pack
	byOwner    map[string][]models.Pack
	byClient   map[int][]models.Pack
	packClient map[[2]int]bool
}

func (f *solderVisibilityStore) ListPublicSolderPacks() ([]models.Pack, error) {
	return f.public, nil
}
func (f *solderVisibilityStore) ListAllSolderPacks(ownerID string) ([]models.Pack, error) {
	return f.byOwner[ownerID], nil
}
func (f *solderVisibilityStore) ListSolderPacksForClient(clientID int) ([]models.Pack, error) {
	return f.byClient[clientID], nil
}
func (f *solderVisibilityStore) IsPackClient(packID, clientID int) (bool, error) {
	return f.packClient[[2]int{packID, clientID}], nil
}

const (
	visOwnerA = "aaaaaaaa-1111-4111-8111-111111111111"
	visOwnerB = "bbbbbbbb-2222-4222-8222-222222222222"
)

// The fixture: one public pack per owner, one private pack owned by A, and one
// hidden pack owned by B that client 7 is whitelisted for.
func solderVisibilityFixture() (*SolderHandler, map[string]models.Pack) {
	packs := map[string]models.Pack{
		"a-public":  {ID: 1, InternalName: "a-public", SolderSlug: "a-public", OwnerID: visOwnerA},
		"b-public":  {ID: 2, InternalName: "b-public", SolderSlug: "b-public", OwnerID: visOwnerB},
		"a-private": {ID: 3, InternalName: "a-private", SolderSlug: "a-private", OwnerID: visOwnerA, Private: true},
		"b-hidden":  {ID: 4, InternalName: "b-hidden", SolderSlug: "b-hidden", OwnerID: visOwnerB, Hidden: true},
	}
	fs := &solderVisibilityStore{
		public:  []models.Pack{packs["a-public"], packs["b-public"]},
		byOwner: map[string][]models.Pack{visOwnerA: {packs["a-private"], packs["a-public"]}},
		byClient: map[int][]models.Pack{
			7: {packs["b-hidden"]},
		},
		packClient: map[[2]int]bool{{4, 7}: true},
	}
	return &SolderHandler{state: &AppState{Store: fs}}, packs
}

func visibleSlugs(t *testing.T, h *SolderHandler, a solderAuth) map[string]bool {
	t.Helper()
	packs, err := h.solderVisiblePacks(a)
	if err != nil {
		t.Fatalf("solderVisiblePacks: %v", err)
	}
	out := make(map[string]bool, len(packs))
	for _, p := range packs {
		if out[p.SolderSlug] {
			t.Errorf("pack %q listed twice", p.SolderSlug)
		}
		out[p.SolderSlug] = true
	}
	return out
}

// Presenting a credential must ADD packs, never remove the public catalogue.
// Classic TechnicSolder walks every pack and only asks the key/client question
// about the gated ones; written as a switch, a launcher that carried a Solder
// key listed that key owner's packs ONLY and every other owner's public pack
// disappeared from it.
func TestASolderCredentialAddsPacksAndNeverHidesThePublicOnes(t *testing.T) {
	h, _ := solderVisibilityFixture()

	tests := []struct {
		name string
		auth solderAuth
		want []string
	}{
		{
			name: "no credential sees the public catalogue",
			auth: solderAuth{},
			want: []string{"a-public", "b-public"},
		},
		{
			name: "a key adds its owner's gated pack",
			auth: solderAuth{hasKey: true, ownerID: visOwnerA},
			want: []string{"a-public", "b-public", "a-private"},
		},
		{
			name: "a client id adds its whitelisted pack",
			auth: solderAuth{clientID: 7},
			want: []string{"a-public", "b-public", "b-hidden"},
		},
		{
			name: "a key and a client id together add both",
			auth: solderAuth{hasKey: true, ownerID: visOwnerA, clientID: 7},
			want: []string{"a-public", "b-public", "a-private", "b-hidden"},
		},
		{
			name: "a key for an owner with nothing gated still sees the catalogue",
			auth: solderAuth{hasKey: true, ownerID: visOwnerB},
			want: []string{"a-public", "b-public"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := visibleSlugs(t, h, tt.auth)
			for _, want := range tt.want {
				if !got[want] {
					t.Errorf("pack %q is missing from the listing", want)
				}
			}
			if len(got) != len(tt.want) {
				t.Errorf("listing has %d packs, want %d: %v", len(got), len(tt.want), got)
			}
		})
	}
}

// The merged list has to stay ordered by internal name, or the unlocked packs
// simply trail the public ones in the launcher.
func TestTheMergedSolderListingStaysSorted(t *testing.T) {
	h, _ := solderVisibilityFixture()
	packs, err := h.solderVisiblePacks(solderAuth{hasKey: true, ownerID: visOwnerA, clientID: 7})
	if err != nil {
		t.Fatalf("solderVisiblePacks: %v", err)
	}
	for i := 1; i < len(packs); i++ {
		if packs[i-1].InternalName > packs[i].InternalName {
			t.Fatalf("listing is not sorted: %q before %q", packs[i-1].InternalName, packs[i].InternalName)
		}
	}
}

// Direct access to one gated pack. The key branch used to RETURN its verdict,
// so a caller who had both a key and a whitelisted client got a flat "no" for
// every pack outside their own key owner and the whitelist was never consulted.
func TestAKeyDoesNotSuppressTheClientWhitelist(t *testing.T) {
	h, packs := solderVisibilityFixture()
	bHidden := packs["b-hidden"]

	both := solderAuth{hasKey: true, ownerID: visOwnerA, clientID: 7}
	if !h.canAccessPack(both, bHidden.ID, bHidden.OwnerID) {
		t.Error("a whitelisted client was refused because it also presented a key of its own")
	}
	if !h.canAccessPack(solderAuth{clientID: 7}, bHidden.ID, bHidden.OwnerID) {
		t.Error("the client whitelist alone must still unlock the pack")
	}

	// The key stays owner-scoped (BC5): it must not reach another owner's pack.
	if h.canAccessPack(solderAuth{hasKey: true, ownerID: visOwnerA}, bHidden.ID, bHidden.OwnerID) {
		t.Error("a key unlocked a pack owned by someone else")
	}
	// And no credential at all unlocks nothing.
	if h.canAccessPack(solderAuth{}, bHidden.ID, bHidden.OwnerID) {
		t.Error("a gated pack was unlocked with no credential")
	}
}
