package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dylaris-core/models"
	"dylaris-core/services/storagemigrate"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newStorageMigrationTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// smDataSet is a tiny in-memory DataSet for the job tests. It is separate
// from storagemigrate's memDataSet (which is unexported and test-only in that
// package) and lives only here.
type smDataSet struct {
	mu      sync.Mutex
	id      string
	label   string
	objects map[string][]byte
	// block, when non-nil, makes Open wait until it is closed - used to hold
	// a job inside a phase while the test cancels it.
	block chan struct{}
	// deleteFailAfter, when > 0, makes Delete succeed for that many keys and
	// then fail. It models the real DeleteSource failure shape: the loop
	// returns on the FIRST error with everything before it already destroyed.
	deleteFailAfter int
	deleteErr       error
	deleteCalls     int
}

func newSMDataSet(id, label string) *smDataSet {
	return &smDataSet{id: id, label: label, objects: map[string][]byte{}}
}

func (d *smDataSet) put(k, v string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.objects[k] = []byte(v)
}

func (d *smDataSet) snapshot() map[string]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]string{}
	for k, v := range d.objects {
		out[k] = string(v)
	}
	return out
}

func (d *smDataSet) ID() string    { return d.id }
func (d *smDataSet) Label() string { return d.label }

func (d *smDataSet) List(context.Context) ([]storagemigrate.ObjectRef, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []storagemigrate.ObjectRef{}
	for k, v := range d.objects {
		out = append(out, storagemigrate.ObjectRef{Key: k, Size: int64(len(v))})
	}
	sortObjectRefs(out)
	return out, nil
}

func (d *smDataSet) Open(_ context.Context, key string) (io.ReadCloser, error) {
	if d.block != nil {
		<-d.block
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.objects[key]
	if !ok {
		return nil, fmt.Errorf("smDataSet open %s: %w", key, fs.ErrNotExist)
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), b...))), nil
}

func (d *smDataSet) Write(_ context.Context, key string, r io.Reader, _ int64) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.objects[key] = b
	return nil
}

func (d *smDataSet) Delete(_ context.Context, key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deleteCalls++
	if d.deleteErr != nil && d.deleteCalls > d.deleteFailAfter {
		return d.deleteErr
	}
	delete(d.objects, key)
	return nil
}

func sortObjectRefs(refs []storagemigrate.ObjectRef) {
	sort.Slice(refs, func(i, j int) bool { return refs[i].Key < refs[j].Key })
}

// smResolver hands out fixed data sets by id.
//
// adHoc is the data set an ad-hoc TargetConfig resolves to, and switchErr /
// switched model the switching_config phase, so the job's phase ordering can
// be tested without any handlers, settings store or real backend.
type smResolver struct {
	sets   map[string]*smDataSet
	labels map[string]string
	err    error

	adHoc      *smDataSet
	adHocLabel string
	resolveErr error
	switchErr  error
	// distinctErr is what EnsureDistinctDataSetLocations returns, i.e. the
	// row-to-row same-location refusal the real resolver computes from the two
	// data sets' physical locations.
	distinctErr    error
	distinctCalled int

	mu           sync.Mutex
	switched     bool
	switchedCfg  StorageTargetConfig
	switchCalled int
	// deleteObserved records what the source held at the moment SwitchConfig
	// was called, so a test can prove nothing was deleted before the switch.
	sourceAtSwitch map[string]string
	sourceSnapshot func() map[string]string
}

func (r *smResolver) List(context.Context) ([]StorageDataSetInfo, error) {
	out := []StorageDataSetInfo{}
	for id, ds := range r.sets {
		out = append(out, StorageDataSetInfo{ID: id, Label: ds.Label(), BackendLabel: r.labels[id], Migratable: true, SupportsTargetConfig: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *smResolver) Resolve(_ context.Context, id string) (storagemigrate.DataSet, string, error) {
	if r.err != nil {
		return nil, "", r.err
	}
	ds, ok := r.sets[id]
	if !ok {
		return nil, "", fmt.Errorf("unknown data set %q", id)
	}
	return ds, r.labels[id], nil
}

func (r *smResolver) ResolveTarget(_ context.Context, _ string, cfg StorageTargetConfig) (storagemigrate.DataSet, string, error) {
	if r.resolveErr != nil {
		return nil, "", r.resolveErr
	}
	if r.adHoc == nil {
		return nil, "", errors.New("no ad-hoc target configured in this fake")
	}
	// The label a real resolver returns is credential-free; mirror that here so
	// the no-secrets test is meaningful rather than accidentally passing.
	label := r.adHocLabel
	if label == "" {
		label = "s3:" + cfg.S3Endpoint + "/" + cfg.S3Bucket
	}
	return r.adHoc, label, nil
}

func (r *smResolver) EnsureDistinctDataSetLocations(_ context.Context, _, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.distinctCalled++
	return r.distinctErr
}

func (r *smResolver) distinctChecks() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.distinctCalled
}

func (r *smResolver) SwitchConfig(_ context.Context, _ string, cfg StorageTargetConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.switchCalled++
	if r.sourceSnapshot != nil {
		r.sourceAtSwitch = r.sourceSnapshot()
	}
	if r.switchErr != nil {
		return r.switchErr
	}
	r.switched = true
	r.switchedCfg = cfg
	return nil
}

func (r *smResolver) state() (called int, switched bool, cfg StorageTargetConfig, atSwitch map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.switchCalled, r.switched, r.switchedCfg, r.sourceAtSwitch
}

// smStore is an in-memory storagemigrate.ManifestStore.
type smStore struct {
	mu        sync.Mutex
	nextID    int
	manifests map[int]*models.StorageManifest
	entries   map[int][]models.StorageManifestEntry
}

func newSMStore() *smStore {
	return &smStore{nextID: 1, manifests: map[int]*models.StorageManifest{}, entries: map[int][]models.StorageManifestEntry{}}
}

func (s *smStore) CreateStorageManifest(m *models.StorageManifest, entries []models.StorageManifestEntry) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++
	cp := *m
	cp.ID = id
	s.manifests[id] = &cp
	s.entries[id] = append([]models.StorageManifestEntry(nil), entries...)
	m.ID = id
	return id, nil
}

func (s *smStore) GetStorageManifest(id int) (*models.StorageManifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.manifests[id], nil
}

func (s *smStore) ListStorageManifestEntries(id int) ([]models.StorageManifestEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[id], nil
}

// waitForPhase polls GetJob until the job reaches one of want, or the
// deadline passes.
func waitForPhase(t *testing.T, svc *StorageMigrationService, want ...StorageMigrationPhase) *StorageMigrationJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, ok, err := svc.GetJob(context.Background())
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if ok {
			for _, w := range want {
				if job.Phase == w {
					return job
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	job, _, _ := svc.GetJob(context.Background())
	t.Fatalf("job never reached %v; last = %+v", want, job)
	return nil
}

func TestStorageMigrationRequest_Validate(t *testing.T) {
	cases := []struct {
		name    string
		req     StorageMigrationRequest
		wantErr bool
	}{
		{"migrate ok", StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "library", VerifyMode: "full"}, false},
		{"manifest ok", StorageMigrationRequest{Kind: StorageJobManifest, DataSet: "library"}, false},
		{"verify ok", StorageMigrationRequest{Kind: StorageJobVerify, DataSet: "library", VerifyMode: "sample", ManifestID: 3}, false},
		{"unknown kind", StorageMigrationRequest{Kind: "wipe", DataSet: "library"}, true},
		{"missing data set", StorageMigrationRequest{Kind: StorageJobManifest}, true},
		{"migrate without target", StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full"}, true},
		{"migrate with bad verify mode", StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "library", VerifyMode: "partial"}, true},
		{"verify without manifest id", StorageMigrationRequest{Kind: StorageJobVerify, DataSet: "library", VerifyMode: "full"}, true},
		// Safety invariant 2, enforced at the API boundary and not only in the UI.
		{
			"delete source under a sample verify",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "sample", DeleteSource: true,
				TargetConfig: &StorageTargetConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}},
			true,
		},
		{
			"delete source under a full verify",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", DeleteSource: true,
				TargetConfig: &StorageTargetConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}},
			false,
		},
		{
			// Only the targetConfig form lets the engine repoint the active
			// config, and only a settled config can authorize a delete. With a
			// targetDataSet target the operator repoints the consuming
			// subsystem afterwards, so a delete here would run BEFORE that
			// repoint and orphan live references.
			"delete source with a target DATA SET rather than a target config",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "ticket-attachments",
				VerifyMode: "full", DeleteSource: true},
			true,
		},
		// deleteSource is meaningless without a copy phase.
		{"delete source on a manifest job", StorageMigrationRequest{Kind: StorageJobManifest, DataSet: "library", DeleteSource: true}, true},

		// --- ad-hoc target config (plan-author note 11) ---
		{
			"migrate with an ad-hoc s3 target config",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full",
				TargetConfig: &StorageTargetConfig{Backend: "s3", S3Bucket: "new", S3AccessKey: "k", S3SecretKey: "s"}},
			false,
		},
		{
			"migrate with an ad-hoc path target config",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full",
				TargetConfig: &StorageTargetConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}},
			false,
		},
		{
			// Ambiguous: which one wins? Refuse rather than pick.
			"migrate naming BOTH a target data set and a target config",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "library-target",
				VerifyMode: "full", TargetConfig: &StorageTargetConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}},
			true,
		},
		{
			"migrate naming NEITHER a target data set nor a target config",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full"},
			true,
		},
		{
			"a target config is meaningless on a manifest job",
			StorageMigrationRequest{Kind: StorageJobManifest, DataSet: "library",
				TargetConfig: &StorageTargetConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}},
			true,
		},
		{
			"a target config is meaningless on a verify job",
			StorageMigrationRequest{Kind: StorageJobVerify, DataSet: "library", VerifyMode: "full", ManifestID: 1,
				TargetConfig: &StorageTargetConfig{Backend: "path", Path: "/mnt/new", PathConfirmed: true}},
			true,
		},
		{
			// Shape only: the deep validation is validateCoreStorageConfig, in
			// handlers (Task 14). Validate rejects an obviously empty backend
			// here so a malformed body never reaches the resolver.
			"migrate with an empty target-config backend",
			StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full",
				TargetConfig: &StorageTargetConfig{}},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.req.Validate(); (err != nil) != c.wantErr {
				t.Fatalf("Validate(%+v) err = %v, wantErr %v", c.req, err, c.wantErr)
			}
		})
	}
}

func TestStorageMigrationInProgress(t *testing.T) {
	live := []StorageMigrationPhase{
		StoragePhasePreparing, StoragePhaseManifesting, StoragePhaseCopying,
		StoragePhaseVerifying, StoragePhaseSwitchingConfig, StoragePhaseDeleting,
	}
	for _, p := range live {
		if !StorageMigrationInProgress(p) {
			t.Errorf("StorageMigrationInProgress(%q) = false, want true", p)
		}
	}
	for _, p := range []StorageMigrationPhase{StoragePhaseDone, StoragePhaseFailed, StoragePhaseCancelled, ""} {
		if StorageMigrationInProgress(p) {
			t.Errorf("StorageMigrationInProgress(%q) = true, want false", p)
		}
	}
}

func TestStorageMigration_ManifestJobRunsToDone(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library")
	src.put("a.jar", "aaa")
	src.put("b.jar", "bbbb")
	st := newSMStore()
	svc := NewStorageMigrationService(rdb, st, &smResolver{
		sets:   map[string]*smDataSet{"library": src},
		labels: map[string]string{"library": "path:/mnt/shared/library"},
	})

	job, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobManifest, DataSet: "library",
	}, "admin-uuid", "root")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if job.Phase != StoragePhasePreparing {
		t.Errorf("initial phase = %q, want preparing", job.Phase)
	}

	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}
	if done.ManifestID == 0 {
		t.Error("ManifestID = 0, want the captured manifest id")
	}
	if done.ObjectsTotal != 2 || done.ObjectsDone != 2 {
		t.Errorf("objects = %d/%d, want 2/2", done.ObjectsDone, done.ObjectsTotal)
	}
	if done.FinishedAt == nil {
		t.Error("FinishedAt is nil on a finished job")
	}
	if len(done.Log) == 0 {
		t.Error("Log is empty")
	}
}

func TestStorageMigration_MigrateCopiesVerifiesAndLeavesTheSourceAlone(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	src.put("sub/b.jar", "bbbb")
	target := newSMDataSet("library-target", "Library (target)")
	st := newSMStore()
	svc := NewStorageMigrationService(rdb, st, &smResolver{
		sets:   map[string]*smDataSet{"library": src, "library-target": target},
		labels: map[string]string{"library": "path:/old", "library-target": "s3:bucket/library"},
	})

	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "library-target", VerifyMode: "full",
	}, "admin-uuid", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}
	if done.Verify == nil || !done.Verify.OK {
		t.Fatalf("verify = %+v, want a passing report", done.Verify)
	}
	if done.Verify.Mode != storagemigrate.VerifyModeFull {
		t.Errorf("verify mode = %q, want full", done.Verify.Mode)
	}
	got := target.snapshot()
	if got["a.jar"] != "aaa" || got["sub/b.jar"] != "bbbb" {
		t.Errorf("target = %v, want the source contents", got)
	}
	// deleteSource was false, so the source must be intact.
	if len(src.snapshot()) != 2 {
		t.Errorf("source = %v, want both objects still present", src.snapshot())
	}
}

func TestStorageMigration_DeleteSourceRunsOnlyAfterAPassingFullVerify(t *testing.T) {
	// Deleting the source is only ever reachable through the targetConfig form,
	// because only that form lets the engine repoint the active config, and only
	// a settled config authorizes a delete (Validate refuses the targetDataSet +
	// deleteSource shape outright).
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets:       map[string]*smDataSet{"library": src},
		labels:     map[string]string{"library": "path:/old"},
		adHoc:      target,
		adHocLabel: "path:/new",
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", TargetConfig: adHocTargetConfig(),
		VerifyMode: "full", DeleteSource: true,
	}, "admin-uuid", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}
	if done.Verify == nil || !done.Verify.OK || done.Verify.Mode != storagemigrate.VerifyModeFull {
		t.Fatalf("verify = %+v, want a passing FULL report: nothing else may authorize the delete", done.Verify)
	}
	if len(src.snapshot()) != 0 {
		t.Errorf("source = %v, want it emptied after a passing full verify + opt-in", src.snapshot())
	}
	if len(target.snapshot()) != 1 {
		t.Errorf("target = %v, want the migrated object", target.snapshot())
	}
	// The delete is gated on a settled config, so a run that reached the delete
	// must have switched first.
	if !done.ConfigSwitched {
		t.Error("ConfigSwitched = false on a run that deleted the source; the delete may only follow a successful switch")
	}
}

// adHocTarget is the target config used by the ad-hoc-target tests. The secret
// is a sentinel: no test may ever find it in a job record, a log line or a
// verify report.
const adHocSecretSentinel = "SENTINEL-S3-SECRET-DO-NOT-LEAK"

func adHocTargetConfig() *StorageTargetConfig {
	return &StorageTargetConfig{
		Backend:     "s3",
		S3Endpoint:  "https://new.example.com",
		S3Bucket:    "dylaris-new",
		S3Region:    "eu-central-1",
		S3AccessKey: "AKIAEXAMPLEKEY",
		S3SecretKey: adHocSecretSentinel,
		S3Prefix:    "prod",
	}
}

func TestStorageMigration_AdHocTargetCopiesVerifiesAndSwitchesTheConfig(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	src.put("sub/b.jar", "bbbb")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets:       map[string]*smDataSet{"library": src},
		labels:     map[string]string{"library": "path:/mnt/old/library"},
		adHoc:      target,
		adHocLabel: "s3:https://new.example.com/dylaris-new/prod/library",
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full",
		TargetConfig: adHocTargetConfig(),
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}
	got := target.snapshot()
	if got["a.jar"] != "aaa" || got["sub/b.jar"] != "bbbb" {
		t.Errorf("target = %v, want the source contents", got)
	}
	if !done.ConfigSwitched {
		t.Error("ConfigSwitched = false; the active config must be repointed at the target after a passing verification")
	}
	called, switched, cfg, _ := res.state()
	if called != 1 || !switched {
		t.Fatalf("SwitchConfig called %d time(s), switched=%v; want exactly one successful switch", called, switched)
	}
	if cfg.S3Bucket != "dylaris-new" || cfg.S3SecretKey != adHocSecretSentinel {
		t.Errorf("SwitchConfig received %+v, want the full target config including its secret (that write path is the ONLY place the secret may land)", cfg)
	}
	// deleteSource was false, so the source is intact and now duplicated.
	if len(src.snapshot()) != 2 {
		t.Errorf("source = %v, want both objects still present", src.snapshot())
	}
	if done.TargetLabel != "s3:https://new.example.com/dylaris-new/prod/library" {
		t.Errorf("TargetLabel = %q, want the credential-free resolver label", done.TargetLabel)
	}
}

func TestStorageMigration_DeleteNeverPrecedesTheConfigSwitch(t *testing.T) {
	// The ordering property, observed rather than assumed: the resolver records
	// what the source held at the instant SwitchConfig was called. If any delete
	// had run first, that snapshot would be short.
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	src.put("b.jar", "bb")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets:           map[string]*smDataSet{"library": src},
		labels:         map[string]string{"library": "path:/mnt/old/library"},
		adHoc:          target,
		adHocLabel:     "s3:https://new.example.com/dylaris-new/prod/library",
		sourceSnapshot: src.snapshot,
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", DeleteSource: true,
		TargetConfig: adHocTargetConfig(),
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}

	_, switched, _, atSwitch := res.state()
	if !switched {
		t.Fatal("the config was never switched")
	}
	if len(atSwitch) != 2 {
		t.Fatalf("source held %v at the moment of the switch, want both objects: a delete ran BEFORE the switch, which would leave the live config pointing at deleted data", atSwitch)
	}
	if len(src.snapshot()) != 0 {
		t.Errorf("source = %v, want it emptied after the switch", src.snapshot())
	}
	if len(target.snapshot()) != 2 {
		t.Errorf("target = %v, want both objects", target.snapshot())
	}
}

func TestStorageMigration_AFailedConfigSwitchDeletesNothingAndLeavesBothCopies(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	src.put("b.jar", "bb")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets:       map[string]*smDataSet{"library": src},
		labels:     map[string]string{"library": "path:/mnt/old/library"},
		adHoc:      target,
		adHocLabel: "s3:https://new.example.com/dylaris-new/prod/library",
		switchErr:  errors.New("settings store unavailable"),
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", DeleteSource: true,
		TargetConfig: adHocTargetConfig(),
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitForPhase(t, svc, StoragePhaseFailed, StoragePhaseDone)
	if done.Phase != StoragePhaseFailed {
		t.Fatalf("phase = %q, want failed: a config switch that did not happen must not be reported as success", done.Phase)
	}
	if done.ConfigSwitched {
		t.Error("ConfigSwitched = true after a failed switch")
	}
	if len(src.snapshot()) != 2 {
		t.Errorf("source = %v, want BOTH objects intact: nothing may be deleted when the switch failed", src.snapshot())
	}
	if len(target.snapshot()) != 2 {
		t.Errorf("target = %v, want the copy left in place", target.snapshot())
	}
	// The operator must be told the data is duplicated and which side is live.
	for _, want := range []string{"both", "Nothing was deleted", "path:/mnt/old/library", "s3:https://new.example.com/dylaris-new/prod/library"} {
		if !strings.Contains(done.Error, want) {
			t.Errorf("job error %q is missing %q", done.Error, want)
		}
	}
}

// TestStorageMigration_AFailedDeleteNeverClaimsTheSourceIsIntact drives a
// delete-phase failure all the way through the runner.
//
// fail() used to append "The source was NOT modified" unconditionally, and the
// delete phase is the ONE failure that can only happen after objects were
// destroyed: DeleteSource returns on the first error with everything before it
// already gone, and the runner discarded the partial result. A failure on
// object 40,000 of 50,000 therefore produced a panel stating the source was
// intact while 39,999 objects were permanently gone - the operator's recovery
// model exactly inverted. No test drove this path, which is why every
// task-scoped review saw that line in a context where it was true.
func TestStorageMigration_AFailedDeleteNeverClaimsTheSourceIsIntact(t *testing.T) {
	cases := []struct {
		name string
		// objects in the source; the delete fails after failAfter successes.
		objects   int
		failAfter int
		// wantDeleted is how many objects the report must own up to.
		wantDeleted int
	}{
		{name: "fails on the very first object", objects: 4, failAfter: 0, wantDeleted: 0},
		{name: "fails midway", objects: 4, failAfter: 2, wantDeleted: 2},
		{name: "fails on the last object", objects: 4, failAfter: 3, wantDeleted: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdb := newStorageMigrationTestRedis(t)
			src := newSMDataSet("library", "Library (source)")
			for i := 0; i < c.objects; i++ {
				src.put(fmt.Sprintf("f%02d.jar", i), strings.Repeat("x", i+1))
			}
			src.deleteFailAfter = c.failAfter
			src.deleteErr = errors.New("backend threw 503 SlowDown")
			target := newSMDataSet("library", "Library (ad-hoc target)")
			res := &smResolver{
				sets:       map[string]*smDataSet{"library": src},
				labels:     map[string]string{"library": "path:/mnt/old/library"},
				adHoc:      target,
				adHocLabel: "s3:https://new.example.com/dylaris-new/prod/library",
			}
			svc := NewStorageMigrationService(rdb, newSMStore(), res)

			if _, err := svc.Start(context.Background(), StorageMigrationRequest{
				Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", DeleteSource: true,
				TargetConfig: adHocTargetConfig(),
			}, "admin", "root"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			done := waitForPhase(t, svc, StoragePhaseFailed, StoragePhaseDone)
			if done.Phase != StoragePhaseFailed {
				t.Fatalf("phase = %q, want failed: the delete errored", done.Phase)
			}

			all := strings.Join(done.Log, "\n")
			// 1. The lie. This is the whole finding: the run destroyed objects,
			//    so nothing in the record may say otherwise.
			if strings.Contains(all, "The source was NOT modified") {
				t.Fatalf("a failed DELETE claimed the source was untouched; %d object(s) were already destroyed.\nlog:\n%s", c.wantDeleted, all)
			}
			// 2. The truth it must state instead: the source is partial, and by
			//    how much.
			if !strings.Contains(all, "PARTIALLY DELETED") {
				t.Errorf("the job never told the operator the source is partially deleted.\nlog:\n%s", all)
			}
			if !strings.Contains(all, fmt.Sprintf("%d of %d object(s)", c.wantDeleted, c.objects)) {
				t.Errorf("the job does not surface the partial count %d of %d.\nlog:\n%s", c.wantDeleted, c.objects, all)
			}
			// 3. Observed, not just asserted: the source really did lose exactly
			//    that many objects, so the reported count is the real one.
			if got := len(src.snapshot()); got != c.objects-c.wantDeleted {
				t.Errorf("source holds %d object(s), want %d: the reported partial count and reality disagree", got, c.objects-c.wantDeleted)
			}
		})
	}
}

// TestStorageMigration_FailuresBeforeTheDeleteStillPromiseAnIntactSource is the
// positive control for the test above: splitting fail() must not silently drop
// the reassurance from the phases that genuinely never write to the source.
func TestStorageMigration_FailuresBeforeTheDeleteStillPromiseAnIntactSource(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets:       map[string]*smDataSet{"library": src},
		labels:     map[string]string{"library": "path:/mnt/old/library"},
		adHoc:      target,
		adHocLabel: "path:/new",
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	// A verify against a manifest id that does not exist fails in a phase that
	// has only ever read.
	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobVerify, DataSet: "library", VerifyMode: "full", ManifestID: 4242,
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitForPhase(t, svc, StoragePhaseFailed, StoragePhaseDone)
	if done.Phase != StoragePhaseFailed {
		t.Fatalf("phase = %q, want failed", done.Phase)
	}
	if !strings.Contains(strings.Join(done.Log, "\n"), "The source was NOT modified") {
		t.Errorf("a pre-delete failure no longer states the source is intact; the split dropped a true and useful claim.\nlog:\n%s", strings.Join(done.Log, "\n"))
	}
	if len(src.snapshot()) != 1 {
		t.Errorf("source = %v, want it untouched", src.snapshot())
	}
}

func TestStorageMigration_ARowToRowPairResolvingToOneLocationIsRefused(t *testing.T) {
	// Two DIFFERENT data-set ids can name ONE physical location: several data
	// sets are namespaces inside a single settings-configured backend. Without
	// this refusal the copy skips every object as already identical, the
	// verification compares the location against its own manifest and returns a
	// trivial 100% PASS, and the finish message tells the operator the source
	// may be removed - it is the same directory.
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("modpacks", "Modpacks")
	src.put("a.mrpack", "aaa")
	tgt := newSMDataSet("modpacks@core-storage", "Modpacks on Core file storage")
	res := &smResolver{
		sets:        map[string]*smDataSet{"modpacks": src, "modpacks@core-storage": tgt},
		labels:      map[string]string{"modpacks": "path:/mnt/shared/modpacks", "modpacks@core-storage": "path:/mnt/shared/modpacks"},
		distinctErr: errors.New("core storage: the target resolves to the same location as the source: data sets \"modpacks\" and \"modpacks@core-storage\" are different names for one physical location"),
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	_, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "modpacks", TargetDataSet: "modpacks@core-storage", VerifyMode: "full",
	}, "admin", "root")
	if err == nil {
		t.Fatal("Start err = nil, want the row-to-row same-location refusal surfaced")
	}
	if !strings.Contains(err.Error(), "same location") {
		t.Errorf("Start err = %v, want it to name the same-location refusal", err)
	}
	if res.distinctChecks() != 1 {
		t.Errorf("EnsureDistinctDataSetLocations called %d time(s), want exactly 1", res.distinctChecks())
	}
	if len(src.snapshot()) != 1 {
		t.Errorf("source = %v, want it untouched by a refused start", src.snapshot())
	}
	// A refused start is not a running job.
	if _, err := rdb.Get(context.Background(), storageMigrationLockKey).Result(); err != redis.Nil {
		t.Errorf("the migration lock was left held after a refused start (err = %v)", err)
	}
}

func TestStorageMigration_TheLocationCheckRunsOnlyOnTheRowToRowPath(t *testing.T) {
	// ResolveTarget already owns the refusal for the ad-hoc-config path, so
	// running the row-to-row check there too would ask the resolver a question
	// it has no target data-set id for. Manifest and verify jobs have no target
	// pairing at all.
	cases := []struct {
		name      string
		req       StorageMigrationRequest
		wantCalls int
	}{
		{
			name:      "row to row",
			req:       StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "library-target", VerifyMode: "full"},
			wantCalls: 1,
		},
		{
			name:      "ad-hoc target config",
			req:       StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", TargetConfig: adHocTargetConfig()},
			wantCalls: 0,
		},
		{
			name:      "manifest only",
			req:       StorageMigrationRequest{Kind: StorageJobManifest, DataSet: "library"},
			wantCalls: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdb := newStorageMigrationTestRedis(t)
			src := newSMDataSet("library", "Library")
			src.put("a.jar", "aaa")
			res := &smResolver{
				sets:       map[string]*smDataSet{"library": src, "library-target": newSMDataSet("library-target", "Target")},
				labels:     map[string]string{"library": "path:/old", "library-target": "path:/new"},
				adHoc:      newSMDataSet("library", "Ad-hoc target"),
				adHocLabel: "path:/adhoc",
			}
			svc := NewStorageMigrationService(rdb, newSMStore(), res)
			if _, err := svc.Start(context.Background(), c.req, "admin", "root"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
			if got := res.distinctChecks(); got != c.wantCalls {
				t.Errorf("EnsureDistinctDataSetLocations called %d time(s), want %d", got, c.wantCalls)
			}
		})
	}
}

func TestStorageMigration_TheOrderMessageMatchesTheJobShape(t *testing.T) {
	// The "Order:" line promised a config switch and a delete unconditionally.
	// A row-to-row migrate never enters switching_config and Validate makes
	// deleteSource unrepresentable for it, so both promised steps described
	// machinery that cannot run on that job.
	cases := []struct {
		name       string
		req        StorageMigrationRequest
		wantSubstr []string
		notSubstr  []string
	}{
		{
			name:       "ad-hoc target config: the switch and the delete are both real",
			req:        StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", TargetConfig: adHocTargetConfig()},
			wantSubstr: []string{"switch the active config to the target"},
		},
		{
			name:       "row to row: neither exists",
			req:        StorageMigrationRequest{Kind: StorageJobMigrate, DataSet: "library", TargetDataSet: "library-target", VerifyMode: "full"},
			wantSubstr: []string{"repoints no config and deletes nothing"},
			notSubstr:  []string{"switch the active config to the target"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdb := newStorageMigrationTestRedis(t)
			src := newSMDataSet("library", "Library")
			src.put("a.jar", "aaa")
			res := &smResolver{
				sets:       map[string]*smDataSet{"library": src, "library-target": newSMDataSet("library-target", "Target")},
				labels:     map[string]string{"library": "path:/old", "library-target": "path:/new"},
				adHoc:      newSMDataSet("library", "Ad-hoc target"),
				adHocLabel: "path:/adhoc",
			}
			svc := NewStorageMigrationService(rdb, newSMStore(), res)
			job, err := svc.Start(context.Background(), c.req, "admin", "root")
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			all := strings.Join(job.Log, "\n")
			for _, want := range c.wantSubstr {
				if !strings.Contains(all, want) {
					t.Errorf("start log is missing %q:\n%s", want, all)
				}
			}
			for _, bad := range c.notSubstr {
				if strings.Contains(all, bad) {
					t.Errorf("start log promises %q, which cannot run on this job shape:\n%s", bad, all)
				}
			}
		})
	}
}

func TestStorageMigration_ADeleteRunNeverClaimsTheOldCopyIsGone(t *testing.T) {
	// finish() appends the finish message LAST, and the panel renders the last
	// log line as THE outcome sentence - displacing DeleteSource's own carefully
	// hedged wording at the exact moment the operator decides whether to tear
	// the old bucket down. DeleteSource removes exactly the manifest's keys, so
	// "the old copy has been removed" is not something the engine can observe.
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library (source)")
	src.put("a.jar", "aaa")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets:       map[string]*smDataSet{"library": src},
		labels:     map[string]string{"library": "path:/mnt/old/library"},
		adHoc:      target,
		adHocLabel: "path:/new",
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)
	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full", DeleteSource: true,
		TargetConfig: adHocTargetConfig(),
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}
	last := done.Log[len(done.Log)-1]
	if strings.Contains(last, "the old copy has been removed") {
		t.Errorf("the outcome sentence claims a whole-location emptiness the engine cannot observe: %q", last)
	}
	for _, want := range []string{"objects named in the manifest were deleted from the source", "after the manifest was captured is untouched"} {
		if !strings.Contains(last, want) {
			t.Errorf("outcome sentence %q is missing %q", last, want)
		}
	}
}

func TestStorageMigration_ARefusedTargetFailsStartBeforeAnythingIsRead(t *testing.T) {
	// The resolver refuses a same-location target (Task 14). Start must surface
	// that as an error and never take the lock or touch an object.
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library")
	src.put("a.jar", "aaa")
	res := &smResolver{
		sets:       map[string]*smDataSet{"library": src},
		labels:     map[string]string{"library": "path:/mnt/old/library"},
		resolveErr: errors.New("the target resolves to the same location as the source"),
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)

	_, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full",
		TargetConfig: adHocTargetConfig(),
	}, "admin", "root")
	if err == nil {
		t.Fatal("Start err = nil, want the resolver's refusal surfaced")
	}
	if !strings.Contains(err.Error(), "same location") {
		t.Errorf("Start err = %v, want it to name the same-location refusal", err)
	}
	if len(src.snapshot()) != 1 {
		t.Errorf("source = %v, want it untouched", src.snapshot())
	}
	// The cluster lock must be free: a refused start is not a running job.
	if _, err := rdb.Get(context.Background(), storageMigrationLockKey).Result(); err != redis.Nil {
		t.Errorf("the migration lock was left held after a refused start (err = %v)", err)
	}
}

func TestStorageMigration_SecondStartWhileRunningReturns409Sentinel(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library")
	src.put("a.jar", "aaa")
	src.block = make(chan struct{}) // hold the job inside manifesting
	t.Cleanup(func() { close(src.block) })

	svc := NewStorageMigrationService(rdb, newSMStore(), &smResolver{
		sets:   map[string]*smDataSet{"library": src},
		labels: map[string]string{"library": "path:/x"},
	})
	req := StorageMigrationRequest{Kind: StorageJobManifest, DataSet: "library"}
	if _, err := svc.Start(context.Background(), req, "admin", "root"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := svc.Start(context.Background(), req, "admin", "root"); !errors.Is(err, ErrStorageMigrationRunning) {
		t.Fatalf("second Start err = %v, want ErrStorageMigrationRunning", err)
	}
}

func TestStorageMigration_CancelUnwindsToCancelled(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library")
	for i := 0; i < 20; i++ {
		src.put(fmt.Sprintf("f%02d.jar", i), strings.Repeat("x", i+1))
	}
	// Hold the job inside manifesting, on its very first object read, until the
	// cancel has been recorded. Without this the 20 tiny in-memory objects race
	// the Cancel call to the finish line and the assertion below would accept a
	// runner that ignores cancellation entirely.
	src.block = make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(src.block) }) }
	t.Cleanup(release)

	svc := NewStorageMigrationService(rdb, newSMStore(), &smResolver{
		sets:   map[string]*smDataSet{"library": src},
		labels: map[string]string{"library": "path:/x"},
	})

	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobManifest, DataSet: "library",
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForPhase(t, svc, StoragePhaseManifesting)
	if err := svc.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	release() // the cancel is now durable; let the run reach its next object boundary

	done := waitForPhase(t, svc, StoragePhaseCancelled, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseCancelled {
		t.Fatalf("phase = %q (error %q), want cancelled: the job was pinned inside manifesting until the cancel was recorded, so it cannot have beaten it", done.Phase, done.Error)
	}
	// The source is untouched - manifesting only reads.
	if len(src.snapshot()) != 20 {
		t.Errorf("source lost objects during a cancelled run: %d left, want 20", len(src.snapshot()))
	}
}

func TestStorageMigration_CancelIsRefusedInTheNonCancellablePhases(t *testing.T) {
	// switching_config and deleting_source are past the point where unwinding is
	// safe: a half-applied switch, like a half-cancelled delete, is strictly
	// worse than a completed one. Cancel must refuse them AND must not leave the
	// flag behind, or the runner would act on it at its next boundary anyway.
	cases := []struct {
		phase       StorageMigrationPhase
		wantRefused bool
	}{
		{StoragePhaseSwitchingConfig, true},
		{StoragePhaseDeleting, true},
		// Positive controls: the cancellable phases must still be cancellable,
		// so a blanket refusal cannot pass this test.
		{StoragePhaseManifesting, false},
		{StoragePhaseCopying, false},
	}
	for _, c := range cases {
		t.Run(string(c.phase), func(t *testing.T) {
			ctx := context.Background()
			rdb := newStorageMigrationTestRedis(t)
			svc := NewStorageMigrationService(rdb, newSMStore(), &smResolver{sets: map[string]*smDataSet{}})

			b, _ := json.Marshal(&StorageMigrationJob{
				ID: "job-1", Kind: StorageJobMigrate, Phase: c.phase, DataSet: "library",
				StartedAt: time.Now(), UpdatedAt: time.Now(),
			})
			if err := rdb.Set(ctx, storageMigrationJobKey, b, storageJobTTL).Err(); err != nil {
				t.Fatalf("seed job: %v", err)
			}

			err := svc.Cancel(ctx)
			flag, flagErr := rdb.Get(ctx, storageMigrationCancelKey).Result()

			if c.wantRefused {
				if err == nil {
					t.Fatalf("Cancel in %q returned nil; that phase is past the point of no return", c.phase)
				}
				if flagErr != redis.Nil {
					t.Fatalf("Cancel in %q was refused but left the cancel flag %q behind (err %v); the runner would act on it at its next boundary", c.phase, flag, flagErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Cancel in %q err = %v, want it accepted", c.phase, err)
			}
			if flag != "job-1" {
				t.Fatalf("cancel flag = %q (err %v), want the job id recorded", flag, flagErr)
			}
			ttl, terr := rdb.TTL(ctx, storageMigrationCancelKey).Result()
			if terr != nil {
				t.Fatalf("TTL: %v", terr)
			}
			if ttl <= storageMigrationLockTTL {
				t.Errorf("cancel flag TTL = %s, want longer than the lock TTL %s: the heartbeat refreshes the lock but not this key, so a cancel issued during a long single-object copy must not expire before the next boundary", ttl, storageMigrationLockTTL)
			}
		})
	}
}

func TestStorageMigration_CancelWithNoJobIsAnError(t *testing.T) {
	svc := NewStorageMigrationService(newStorageMigrationTestRedis(t), newSMStore(), &smResolver{sets: map[string]*smDataSet{}})
	if err := svc.Cancel(context.Background()); !errors.Is(err, ErrNoStorageMigrationJob) {
		t.Fatalf("Cancel with no job err = %v, want ErrNoStorageMigrationJob", err)
	}
}

func TestStorageMigration_GetJobBeforeAnyRun(t *testing.T) {
	svc := NewStorageMigrationService(newStorageMigrationTestRedis(t), newSMStore(), &smResolver{sets: map[string]*smDataSet{}})
	job, ok, err := svc.GetJob(context.Background())
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if ok || job != nil {
		t.Fatalf("GetJob = (%+v, %v), want (nil, false) before any run", job, ok)
	}
}

func TestStorageMigration_StaleIsComputedOnRead(t *testing.T) {
	rdb := newStorageMigrationTestRedis(t)
	svc := NewStorageMigrationService(rdb, newSMStore(), &smResolver{sets: map[string]*smDataSet{}})

	// Persist a live-looking job whose heartbeat stopped long ago.
	stale := &StorageMigrationJob{
		ID:        "deadbeef",
		Kind:      StorageJobMigrate,
		Phase:     StoragePhaseCopying,
		DataSet:   "library",
		StartedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-storageStaleAfter - time.Minute),
	}
	b, _ := json.Marshal(stale)
	if err := rdb.Set(context.Background(), storageMigrationJobKey, b, storageJobTTL).Err(); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	job, ok, err := svc.GetJob(context.Background())
	if err != nil || !ok {
		t.Fatalf("GetJob = (%v, %v, %v)", job, ok, err)
	}
	if !job.Stale {
		t.Error("Stale = false for a job whose heartbeat stopped; admins would be misled")
	}
}

func TestStorageMigrationJob_AppendLogIsCapped(t *testing.T) {
	j := &StorageMigrationJob{}
	for i := 0; i < storageMaxLogLines+250; i++ {
		j.appendLog(fmt.Sprintf("line %d", i))
	}
	if len(j.Log) != storageMaxLogLines {
		t.Fatalf("len(Log) = %d, want the cap %d", len(j.Log), storageMaxLogLines)
	}
	// The cap keeps the MOST RECENT lines.
	if j.Log[len(j.Log)-1] != fmt.Sprintf("line %d", storageMaxLogLines+249) {
		t.Errorf("last line = %q, want the newest", j.Log[len(j.Log)-1])
	}
}

func TestStorageMigration_NoSecretsInThePersistedJob(t *testing.T) {
	// Safety invariant 6: credentials never enter a job record or a log line.
	// This drives a FULL ad-hoc-target migrate, because that is the only flow
	// where a live S3 secret is ever handed to the engine at all. The secret
	// must survive nowhere except the resolver's SwitchConfig call, which is
	// the normal core-storage settings write path.
	rdb := newStorageMigrationTestRedis(t)
	src := newSMDataSet("library", "Library")
	src.put("a.jar", "aaa")
	src.put("sub/b.jar", "bbbb")
	target := newSMDataSet("library", "Library (ad-hoc target)")
	res := &smResolver{
		sets: map[string]*smDataSet{"library": src},
		// The resolver is what produces the backend label; a correct
		// implementation never puts credentials in it.
		labels:     map[string]string{"library": "s3:https://s3.example.com/bucket/library"},
		adHoc:      target,
		adHocLabel: "s3:https://new.example.com/dylaris-new/prod/library",
	}
	svc := NewStorageMigrationService(rdb, newSMStore(), res)
	if _, err := svc.Start(context.Background(), StorageMigrationRequest{
		Kind: StorageJobMigrate, DataSet: "library", VerifyMode: "full",
		TargetConfig: adHocTargetConfig(),
	}, "admin", "root"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := waitForPhase(t, svc, StoragePhaseDone, StoragePhaseFailed)
	if done.Phase != StoragePhaseDone {
		t.Fatalf("phase = %q (error %q), want done", done.Phase, done.Error)
	}

	// 1. The raw Redis blob: this is what every settings.read holder can fetch,
	//    and it lives for storageJobTTL.
	raw, err := rdb.Get(context.Background(), storageMigrationJobKey).Result()
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if strings.Contains(raw, adHocSecretSentinel) {
		t.Fatalf("the persisted job contains the target's S3 secret:\n%s", raw)
	}
	if strings.Contains(raw, "AKIAEXAMPLEKEY") {
		t.Fatalf("the persisted job contains the target's S3 access key:\n%s", raw)
	}
	// 2. Every log line individually, so a secret cannot hide behind JSON
	//    escaping in the blob check above.
	for i, line := range done.Log {
		if strings.Contains(line, adHocSecretSentinel) || strings.Contains(line, "AKIAEXAMPLEKEY") {
			t.Fatalf("log line %d leaks a credential: %q", i, line)
		}
	}
	// 3. The verify report, which is served alongside the job.
	if done.Verify != nil {
		for i, line := range done.Verify.Log {
			if strings.Contains(line, adHocSecretSentinel) {
				t.Fatalf("verify log line %d leaks the secret: %q", i, line)
			}
		}
	}
	// 4. Positive control: the secret DID reach the one place it is allowed to,
	//    so this test cannot pass merely because nothing was ever set.
	if _, _, cfg, _ := res.state(); cfg.S3SecretKey != adHocSecretSentinel {
		t.Fatalf("SwitchConfig got secret %q, want the sentinel - without this the leak checks above prove nothing", cfg.S3SecretKey)
	}
}
