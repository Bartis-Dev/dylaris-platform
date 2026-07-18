package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dylaris-core/storage"
)

// Setting keys for the single shared "Core file storage" config.
const (
	keyCoreStorageBackend     = "core_storage_backend"
	keyCoreStoragePath        = "core_storage_path"
	keyCoreStoragePathConfirm = "core_storage_path_confirmed"
	keyCoreStorageS3Endpoint  = "core_storage_s3_endpoint"
	keyCoreStorageS3Bucket    = "core_storage_s3_bucket"
	keyCoreStorageS3Region    = "core_storage_s3_region"
	keyCoreStorageS3AccessKey = "core_storage_s3_access_key"
	keyCoreStorageS3SecretKey = "core_storage_s3_secret_key"
	keyCoreStorageS3PathStyle = "core_storage_s3_path_style"
	keyCoreStorageS3Prefix    = "core_storage_s3_prefix"
)

// Sub-prefixes carve one shared provider into per-subsystem namespaces.
const (
	CoreStoragePrefixLibrary     = "library"
	CoreStoragePrefixAttachments = "ticket-attachments"
	CoreStoragePrefixBackups     = "ticket-backups"
)

// CoreStorageConfig is the wire + persisted shape of the shared config. The S3
// secret is write-only: never emitted on read; S3SecretSet tells the UI whether
// one is already stored.
type CoreStorageConfig struct {
	Backend       string `json:"backend"` // "path" | "s3"
	Path          string `json:"path"`
	PathConfirmed bool   `json:"pathConfirmed"`
	S3Endpoint    string `json:"s3Endpoint"`
	S3Bucket      string `json:"s3Bucket"`
	S3Region      string `json:"s3Region"`
	S3AccessKey   string `json:"s3AccessKey"`
	S3SecretKey   string `json:"s3SecretKey,omitempty"`
	S3PathStyle   bool   `json:"s3PathStyle"`
	S3Prefix      string `json:"s3Prefix"`
	S3SecretSet   bool   `json:"s3SecretSet"`
}

// validateCoreStorageConfig enforces the rules the panel also mirrors: path
// must be absolute AND operator-confirmed; s3 must have bucket + credentials.
func validateCoreStorageConfig(c CoreStorageConfig) error {
	switch c.Backend {
	case "path", "local":
		if c.Path == "" {
			return fmt.Errorf("core storage: path is required for the filesystem backend")
		}
		// The configured path is always a path on the Core container's
		// filesystem (Linux; this ships as a Docker image only), so the
		// absolute-path check is Linux-explicit rather than filepath.IsAbs
		// (which is host-OS-dependent and would treat "/mnt/shared" as
		// relative when the handler itself is built/tested on Windows).
		// Same precedent as storage/modpack.IsUnsafeEntryPath.
		if !strings.HasPrefix(c.Path, "/") {
			return fmt.Errorf("core storage: path must be absolute")
		}
		if !c.PathConfirmed {
			return fmt.Errorf("core storage: the shared-path confirmation is required")
		}
		return nil
	case "s3":
		if c.S3Bucket == "" {
			return fmt.Errorf("core storage: s3 bucket is required")
		}
		if c.S3AccessKey == "" || c.S3SecretKey == "" {
			return fmt.Errorf("core storage: s3 access key + secret are required")
		}
		return nil
	default:
		return fmt.Errorf("core storage: backend must be \"path\" or \"s3\"")
	}
}

// LoadCoreStorageConfig reads the persisted config. S3SecretKey is loaded so
// callers that build a provider have it, but handlers must blank it before
// returning to a client. S3SecretSet reflects whether a secret exists.
func (s *AppState) LoadCoreStorageConfig() CoreStorageConfig {
	get := func(k string) string {
		if s.Store == nil {
			return ""
		}
		v, _ := s.Store.GetSetting(k)
		return v
	}
	secret := get(keyCoreStorageS3SecretKey)
	return CoreStorageConfig{
		Backend:       get(keyCoreStorageBackend),
		Path:          get(keyCoreStoragePath),
		PathConfirmed: get(keyCoreStoragePathConfirm) == "true",
		S3Endpoint:    get(keyCoreStorageS3Endpoint),
		S3Bucket:      get(keyCoreStorageS3Bucket),
		S3Region:      get(keyCoreStorageS3Region),
		S3AccessKey:   get(keyCoreStorageS3AccessKey),
		S3SecretKey:   secret,
		S3PathStyle:   get(keyCoreStorageS3PathStyle) == "true",
		S3Prefix:      get(keyCoreStorageS3Prefix),
		S3SecretSet:   secret != "",
	}
}

// CoreStorageConfigured reports whether a valid shared config exists.
func (s *AppState) CoreStorageConfigured() bool {
	return validateCoreStorageConfig(s.LoadCoreStorageConfig()) == nil
}

// buildCoreStorageProvider returns a provider SCOPED to subPrefix. When the
// config is unset/invalid it falls back to today's node-local dylaris_data/<sub>
// dir so existing installs keep browsing/downloading; WRITE endpoints are gated
// separately by RequireCoreStorageConfigured.
func (s *AppState) buildCoreStorageProvider(subPrefix string) (storage.StorageProvider, error) {
	cfg := s.LoadCoreStorageConfig()
	if validateCoreStorageConfig(cfg) != nil {
		baseDir, _ := os.Getwd()
		root := filepath.Join(baseDir, "dylaris_data", subPrefix)
		_ = os.MkdirAll(root, 0755)
		return &storage.LocalProvider{BasePath: root}, nil
	}
	return newStorageProviderForConfig(cfg, subPrefix)
}

// newStorageProviderForConfig builds a StorageProvider strictly from cfg
// (assumed already validated), scoped to subPrefix. Unlike
// buildCoreStorageProvider, this never falls back to the legacy local dir:
// TestConnection needs a hard failure on a broken candidate config, not a
// silent success against the wrong backend.
func newStorageProviderForConfig(cfg CoreStorageConfig, subPrefix string) (storage.StorageProvider, error) {
	if cfg.Backend == "s3" {
		prefix := subPrefix
		if cfg.S3Prefix != "" {
			prefix = cfg.S3Prefix + "/" + subPrefix
		}
		return storage.NewProvider("s3", "", map[string]string{
			storage.OptS3Endpoint:  cfg.S3Endpoint,
			storage.OptS3Bucket:    cfg.S3Bucket,
			storage.OptS3Region:    cfg.S3Region,
			storage.OptS3AccessKey: cfg.S3AccessKey,
			storage.OptS3SecretKey: cfg.S3SecretKey,
			storage.OptS3PathStyle: boolStr(cfg.S3PathStyle),
			storage.OptS3Prefix:    prefix,
		})
	}
	root := filepath.Join(cfg.Path, subPrefix)
	_ = os.MkdirAll(root, 0755)
	return storage.NewProvider("path", root, nil)
}

// --- Admin HTTP surface: config CRUD + test-connection ---

// CoreStorageHandler serves the shared Core file storage config CRUD + probe.
type CoreStorageHandler struct {
	state *AppState
}

func NewCoreStorageHandler(state *AppState) *CoreStorageHandler {
	return &CoreStorageHandler{state: state}
}

// GetConfig GET /api/settings/core-storage - PANEL settings.read (RequireCap
// at the route). The stored S3 secret is never emitted: S3SecretKey is
// blanked (and, thanks to its "omitempty" json tag, omitted entirely from
// the response) while S3SecretSet tells the panel one is already stored.
func (h *CoreStorageHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.state.LoadCoreStorageConfig()
	cfg.S3SecretKey = "" // write-only; never emitted
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"settings": cfg,
	})
}

// normalizeCoreStorageCandidate trims whitespace from every free-text field
// of a submitted candidate, so a clipboard-pasted value (path, or any s3
// field) with leading/trailing whitespace doesn't silently persist as-is and
// break the backend or auth later. SaveConfig and TestConnection both route
// their candidate through this single helper so they cannot drift again.
func normalizeCoreStorageCandidate(c CoreStorageConfig) CoreStorageConfig {
	c.Backend = strings.TrimSpace(c.Backend)
	c.Path = strings.TrimSpace(c.Path)
	c.S3Endpoint = strings.TrimSpace(c.S3Endpoint)
	c.S3Bucket = strings.TrimSpace(c.S3Bucket)
	c.S3AccessKey = strings.TrimSpace(c.S3AccessKey)
	c.S3SecretKey = strings.TrimSpace(c.S3SecretKey)
	return c
}

// mergeCoreStorageCandidate resolves the effective config to act on: an
// empty request-body backend means "nothing submitted", so the caller falls
// back to the stored config entirely.
//
// Otherwise, a blank secret in the candidate reuses the stored secret ONLY
// when the identity-defining fields (S3Endpoint, S3Bucket, S3AccessKey) all
// match the stored config; the caller is expected to have already run the
// candidate through normalizeCoreStorageCandidate so this comparison isn't
// fooled by whitespace. If any identity field differs, the secret is left
// blank so validateCoreStorageConfig rejects the request and the admin must
// re-enter the secret. This matters for two reasons:
//   - Security: settings.write is a delegatable panel capability, not
//     owner-only, and GET always blanks the stored secret. Without this
//     check, a holder of settings.write who cannot read the secret could
//     redirect it to an endpoint/bucket/access-key of their choosing merely
//     by submitting those fields with a blank secret - a credential-
//     rebinding gap (SigV4 signs with the secret, it never goes on the
//     wire, so this is not a plaintext leak, but it lets an attacker's
//     chosen host receive a validly-signed request).
//   - Usability: changing just the access key while leaving the secret
//     blank (because GET hides it) would otherwise silently persist a NEW
//     access key paired with the OLD secret, producing a broken config with
//     no error.
func mergeCoreStorageCandidate(candidate, existing CoreStorageConfig) CoreStorageConfig {
	if candidate.Backend == "" {
		return existing
	}
	identityUnchanged := candidate.S3Endpoint == existing.S3Endpoint &&
		candidate.S3Bucket == existing.S3Bucket &&
		candidate.S3AccessKey == existing.S3AccessKey
	if candidate.S3SecretKey == "" && identityUnchanged {
		candidate.S3SecretKey = existing.S3SecretKey
	}
	return candidate
}

// SaveConfig POST /api/settings/core-storage - PANEL settings.write
// (RequireCap at the route). Validates before persisting anything, so a
// rejected save never partially overwrites a previously-good config.
func (h *CoreStorageHandler) SaveConfig(w http.ResponseWriter, r *http.Request) {
	var req CoreStorageConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	req = normalizeCoreStorageCandidate(req)

	existing := h.state.LoadCoreStorageConfig()
	effective := mergeCoreStorageCandidate(req, existing)
	if err := validateCoreStorageConfig(effective); err != nil {
		sendJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}

	pairs := []struct{ k, v string }{
		{keyCoreStorageBackend, effective.Backend},
		{keyCoreStoragePath, effective.Path},
		{keyCoreStoragePathConfirm, boolStr(effective.PathConfirmed)},
		{keyCoreStorageS3Endpoint, effective.S3Endpoint},
		{keyCoreStorageS3Bucket, effective.S3Bucket},
		{keyCoreStorageS3Region, effective.S3Region},
		{keyCoreStorageS3AccessKey, effective.S3AccessKey},
		{keyCoreStorageS3PathStyle, boolStr(effective.S3PathStyle)},
		{keyCoreStorageS3Prefix, effective.S3Prefix},
	}
	// Only touch the stored secret when the request actually submitted a new
	// one; a blank incoming secret must never overwrite what's already saved.
	if req.S3SecretKey != "" {
		pairs = append(pairs, struct{ k, v string }{keyCoreStorageS3SecretKey, req.S3SecretKey})
	}
	for _, p := range pairs {
		if err := h.state.Store.SetSetting(p.k, p.v); err != nil {
			sendJSONError(w, "Failed to save setting: "+p.k, http.StatusInternalServerError)
			return
		}
	}
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// coreStorageProbePayload is the fixed content the connection-test probe
// writes and expects back verbatim.
const coreStorageProbePayload = "dylaris-probe"

// TestConnection POST /api/settings/core-storage/test - PANEL settings.write
// (RequireCap at the route). Builds a provider from the CANDIDATE config in
// the request body (not the saved one) so an admin can test before saving;
// an empty/absent body falls back to testing the currently-saved config, and
// a blank secret in a submitted candidate reuses the stored one, mirroring
// SaveConfig. The probe writes, reads back and deletes a uniquely-named
// object under a "_probe" sub-prefix so it never touches real Library/ticket
// data.
func (h *CoreStorageHandler) TestConnection(w http.ResponseWriter, r *http.Request) {
	respond := func(ok bool, msg string) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "ok": ok, "message": msg})
	}

	var candidate CoreStorageConfig
	if err := json.NewDecoder(r.Body).Decode(&candidate); err != nil && err != io.EOF {
		sendJSONError(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	candidate = normalizeCoreStorageCandidate(candidate)
	effective := mergeCoreStorageCandidate(candidate, h.state.LoadCoreStorageConfig())

	if err := validateCoreStorageConfig(effective); err != nil {
		respond(false, err.Error())
		return
	}
	prov, err := newStorageProviderForConfig(effective, "_probe")
	if err != nil {
		respond(false, "Could not build provider: "+err.Error())
		return
	}
	ok, msg := probeStorageProvider(prov)
	if effective.Backend == "path" || effective.Backend == "local" {
		// newStorageProviderForConfig(effective, "_probe") MkdirAll'd this
		// exact directory; probeStorageProvider cleans up the object inside
		// it on every return path, so it is empty by now. os.Remove (never
		// RemoveAll) only succeeds on an empty dir, so this can't remove
		// anything else - best-effort, so a tested-then-rejected/never-saved
		// path doesn't keep an empty "_probe" dir behind forever.
		_ = os.Remove(filepath.Join(effective.Path, "_probe"))
	}
	respond(ok, msg)
}

// probeStorageProvider writes, reads back and deletes a uniquely-named probe
// object to verify the backend is reachable and read/write-consistent. The
// probe key is deleted on every path, including a write error, a read error,
// or a read-back/content mismatch, so a broken candidate config never leaves
// a stray object behind.
func probeStorageProvider(prov storage.StorageProvider) (ok bool, message string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	key := "probe-" + hex.EncodeToString(b) + ".txt"

	if err := prov.WriteFile(key, strings.NewReader(coreStorageProbePayload)); err != nil {
		// A failed WriteFile can still leave a truncated object behind
		// (LocalProvider.WriteFile is os.Create + io.Copy, so a mid-copy
		// failure leaves a partial file), so cleanup here too.
		_ = prov.DeletePath(key)
		return false, "Write failed: " + err.Error()
	}
	rc, err := prov.GetFile(key)
	if err != nil {
		_ = prov.DeletePath(key)
		return false, "Read-back failed: " + err.Error()
	}
	got, readErr := io.ReadAll(rc)
	rc.Close()
	if readErr != nil {
		_ = prov.DeletePath(key)
		return false, "Read-back failed: " + readErr.Error()
	}
	if string(got) != coreStorageProbePayload {
		_ = prov.DeletePath(key)
		return false, "Read-back mismatch: storage backend is not consistent"
	}
	if err := prov.DeletePath(key); err != nil {
		return false, "Cleanup failed: " + err.Error()
	}
	return true, "Storage reachable: write, read and delete all succeeded."
}

// --- Admin-triggered migration of existing local data into the configured backend ---

// maxMigrateErrorsPerSubsystem caps how many individual per-file error
// messages migrateLocalDirToProvider collects for one subsystem, so a badly
// broken destination (e.g. every write failing) cannot blow up the response
// body. Failed still counts every failure; only the message list is capped.
const maxMigrateErrorsPerSubsystem = 20

// migrateResult reports what happened while copying one legacy subsystem
// directory (dylaris_data/<sub>) into its configured provider.
//
// OVERWRITE POLICY: SKIP-IF-EXISTS. A file already present at the
// destination key is left untouched and counted as Skipped, never
// overwritten. This is what makes the migration safely re-runnable: a
// partial or interrupted run can simply be triggered again, and only the
// files that did not make it across the first time are attempted again,
// instead of re-transferring everything that already migrated cleanly.
type migrateResult struct {
	Copied  int      `json:"copied"`
	Skipped int      `json:"skipped"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

func appendCappedMigrateError(errs []string, msg string) []string {
	if len(errs) >= maxMigrateErrorsPerSubsystem {
		return errs
	}
	return append(errs, msg)
}

// sameStorageDir reports whether a and b resolve to the identical on-disk
// directory. Used to refuse a migration whose destination is the exact
// directory it would read from: LocalProvider.WriteFile does os.Create
// (truncate) on the destination path, so if that path is the same file
// already open for reading, the "copy" would destroy the very original that
// rule 1 (never touch the source) requires this migration to leave alone.
//
// os.SameFile (device+inode) is tried first so a symlink or a bind mount
// pointing at the same underlying directory is caught too - a plain string
// comparison of the two paths cannot see through either, and this rework's
// entire point is moving to a shared path, so an operator bind-mounting the
// same volume at a second location is a realistic setup, not a hypothetical.
// Falls back to the previous Abs+Clean string comparison only when either
// os.Stat fails (e.g. the destination does not exist yet), since os.SameFile
// requires both FileInfos to exist.
func sameStorageDir(a, b string) bool {
	if infoA, errA := os.Stat(a); errA == nil {
		if infoB, errB := os.Stat(b); errB == nil {
			return os.SameFile(infoA, infoB)
		}
	}
	ca, errA := filepath.Abs(a)
	cb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(ca) == filepath.Clean(cb)
}

// nestedStorageDir reports whether one of a, b lies inside the other's
// directory tree (in either direction; a == b is NOT nested, that's
// sameStorageDir's job). Guards the cheap-but-real case sameStorageDir
// cannot: a destination that sits inside the source tree (or vice versa)
// makes filepath.WalkDir ingest files this same run just wrote into the
// destination, producing bounded duplication in the walk.
func nestedStorageDir(a, b string) bool {
	ca, errA := filepath.Abs(a)
	cb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		ca, cb = a, b
	}
	ca, cb = filepath.Clean(ca), filepath.Clean(cb)
	return isSubPath(ca, cb) || isSubPath(cb, ca)
}

// isSubPath reports whether child is strictly inside parent's directory
// tree. Both arguments must already be Abs+Clean. The separator suffix on
// parent keeps this a directory-boundary-aware prefix check, so e.g.
// "/data" does not spuriously match a sibling "/data2".
func isSubPath(parent, child string) bool {
	if parent == child {
		return false
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

// migrateLocalDirToProvider walks srcRoot and writes every regular file into
// prov under its srcRoot-relative, forward-slash key, skipping any key that
// already exists at the destination (see migrateResult's overwrite-policy
// doc). Missing srcRoot is a no-op (fresh install with nothing to migrate
// yet). The source is only ever opened for reading; nothing under srcRoot is
// removed, moved or modified, so a failed or partial run can always be
// retried, and reverting the storage config rolls back to the untouched
// originals.
//
// A single file's open/write failure is recorded in the result and does NOT
// abort the walk - the rest of the tree is still attempted. The returned
// error is non-nil whenever the result carries at least one failure (a
// per-file failure, or one of the fatal conditions below), so callers can
// treat "err != nil" as "this subsystem did not fully succeed" while still
// having the itemized detail in the result.
func migrateLocalDirToProvider(srcRoot string, prov storage.StorageProvider) (migrateResult, error) {
	var res migrateResult

	if lp, ok := prov.(*storage.LocalProvider); ok {
		if sameStorageDir(srcRoot, lp.BasePath) {
			err := fmt.Errorf("migrate: destination %q is the same directory as the source %q; refusing to copy onto itself", lp.BasePath, srcRoot)
			res.Failed = 1
			res.Errors = []string{err.Error()}
			return res, err
		}
		if nestedStorageDir(srcRoot, lp.BasePath) {
			err := fmt.Errorf("migrate: destination %q is nested inside (or contains) the source %q; refusing to avoid the walk re-ingesting files this run just wrote", lp.BasePath, srcRoot)
			res.Failed = 1
			res.Errors = []string{err.Error()}
			return res, err
		}
	}

	info, statErr := os.Stat(srcRoot)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return res, nil
		}
		res.Failed = 1
		res.Errors = []string{statErr.Error()}
		return res, statErr
	}
	if !info.IsDir() {
		err := fmt.Errorf("migrate: %q is not a directory", srcRoot)
		res.Failed = 1
		res.Errors = []string{err.Error()}
		return res, err
	}

	_ = filepath.WalkDir(srcRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			res.Failed++
			res.Errors = appendCappedMigrateError(res.Errors, p+": "+err.Error())
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcRoot, p)
		if relErr != nil {
			res.Failed++
			res.Errors = appendCappedMigrateError(res.Errors, p+": "+relErr.Error())
			return nil
		}
		key := filepath.ToSlash(rel)

		// Skip-if-exists: StorageProvider has no dedicated Exists check, so
		// GetFile is used as a best-effort probe. This is the SECOND safety
		// layer here (after the same-dir/nested-dir guards above) - do not
		// remove it as "redundant" with those: it is what makes a re-run
		// idempotent instead of overwriting already-migrated files.
		//
		// Only a RECOGNISED not-found may mean "copy it". GetFile can also
		// fail for reasons that have nothing to do with existence - a
		// transient S3 503/timeout/throttle/permission error looks exactly
		// like a missing key to a caller that doesn't distinguish them. If
		// that transient error were treated as "not present yet", a later
		// re-run (this endpoint's entire contract is "safe to re-run") could
		// overwrite a NEWER object at the destination with the STALE legacy
		// copy and report it as a successful Copy - silent destination data
		// loss. So any error that is not errors.Is(err, fs.ErrNotExist) is
		// recorded as a per-file failure and this file is NEVER written.
		rc, getErr := prov.GetFile(key)
		if getErr == nil {
			rc.Close()
			res.Skipped++
			return nil
		}
		if !errors.Is(getErr, fs.ErrNotExist) {
			res.Failed++
			res.Errors = appendCappedMigrateError(res.Errors, key+": destination probe: "+getErr.Error())
			return nil
		}

		f, openErr := os.Open(p)
		if openErr != nil {
			res.Failed++
			res.Errors = appendCappedMigrateError(res.Errors, key+": open source: "+openErr.Error())
			return nil
		}
		writeErr := prov.WriteFile(key, f)
		f.Close()
		if writeErr != nil {
			res.Failed++
			res.Errors = appendCappedMigrateError(res.Errors, key+": "+writeErr.Error())
			return nil
		}
		res.Copied++
		return nil
	})

	// Errors is capped (maxMigrateErrorsPerSubsystem) so a badly broken
	// destination can't blow up the response body, but Failed always counts
	// every failure. When some were dropped, say so explicitly: a caller
	// reading "failed: 500, errors: [20]" with no further hint could easily
	// mistake the truncated list for the complete one.
	if omitted := res.Failed - len(res.Errors); omitted > 0 {
		res.Errors = append(res.Errors, fmt.Sprintf("... %d more errors omitted", omitted))
	}

	if res.Failed > 0 {
		return res, fmt.Errorf("migrate: %d file(s) failed under %q", res.Failed, srcRoot)
	}
	return res, nil
}

// Migrate POST /api/settings/core-storage/migrate - PANEL settings.write.
// Copies the legacy dylaris_data/{library,ticket-attachments,ticket-backups}
// trees into the currently configured provider, scoped per subsystem exactly
// like buildCoreStorageProvider. Requires a valid configured destination:
// migrating into the unconfigured legacy fallback would just copy
// dylaris_data onto itself, so this refuses outright (400) instead of
// running a pointless no-op.
//
// SAFETY: only ever copies. The source trees are never deleted, moved or
// modified, so this is safe to re-run after a partial failure (skip-if-
// exists, see migrateResult) and a bad destination config can always be
// rolled back by reverting the Core file storage settings.
//
// Every subsystem is attempted even if another one fails or refuses (e.g.
// hitting the same-directory guard), so one bad subsystem never hides
// whether the other two succeeded. "success" in the response is true only
// when every subsystem completed with zero failures; a partial result is
// always reported as such, never as success.
func (h *CoreStorageHandler) Migrate(w http.ResponseWriter, r *http.Request) {
	if !h.state.CoreStorageConfigured() {
		sendJSONError(w, "Configure Core file storage before migrating.", http.StatusBadRequest)
		return
	}
	// A wrong (or empty, relative) baseDir here means migrating the wrong
	// tree entirely - this must be a hard failure, never a silent
	// degrade-to-relative-path.
	baseDir, err := os.Getwd()
	if err != nil {
		sendJSONError(w, "Could not determine the legacy data directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	subs := []string{CoreStoragePrefixLibrary, CoreStoragePrefixAttachments, CoreStoragePrefixBackups}
	results := map[string]migrateResult{}
	success := true
	for _, sub := range subs {
		prov, err := h.state.buildCoreStorageProvider(sub)
		if err != nil {
			results[sub] = migrateResult{Failed: 1, Errors: []string{"provider: " + err.Error()}}
			success = false
			continue
		}
		src := filepath.Join(baseDir, "dylaris_data", sub)
		res, migErr := migrateLocalDirToProvider(src, prov)
		results[sub] = res
		if migErr != nil {
			success = false
		}
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": success,
		"results": results,
		"note":    "Original files were left in place. Verify the new backend, then remove the old dirs manually.",
	})
}
