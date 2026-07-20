package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"dylaris-core/services"
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
// server-backups and modpacks are OPT-IN namespaces: they are only used when
// an operator explicitly points that subsystem at the "core-storage" backend.
// CoreStoragePrefixServerBackups is duplicated as storage/backup.CoreStorageSubPrefix
// and CoreStoragePrefixModpacks is duplicated as storage/modpack.CoreStorageSubPrefix
// (neither package may import this one); TestCoreStorageSubPrefixesMatch locks
// both pairs together.
const (
	CoreStoragePrefixLibrary       = "library"
	CoreStoragePrefixAttachments   = "ticket-attachments"
	CoreStoragePrefixBackups       = "ticket-backups"
	CoreStoragePrefixServerBackups = "server-backups"
	CoreStoragePrefixModpacks      = "modpacks"
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
		if err := validateS3Endpoint("core storage", c.S3Endpoint); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("core storage: backend must be \"path\" or \"s3\"")
	}
}

// validateS3Endpoint refuses an endpoint that carries a credential in its
// userinfo component (https://AKIA...:secret@minio.internal). subject names the
// settings group in the error so the operator knows which form to fix.
//
// Why this is a VALIDATION rule and not only a rendering concern: the endpoint
// is persisted verbatim (core_storage_s3_endpoint, modpack_storage_s3_endpoint)
// and it is the raw material for every backend label the migration engine
// writes - into storage_manifests.backend_label (durable, in Postgres), the
// Redis job record (7-day TTL, readable by every settings.read holder), the
// panel job log and the manifest CSV export. The modpack endpoint is echoed
// back by the modpack settings GET on top of that. Sanitizing at render time
// alone leaves the credential sitting in the settings table; refusing it here
// means it never enters the system. storagemigrate.SanitizeBackendLabel is the
// second half of the same guarantee, for configs this check never saw.
//
// It FAILS CLOSED on "@", exactly as sanitizeEndpoint does: a valid S3 endpoint
// host cannot contain one, and a password with a space or a control character
// defeats url.Parse, so "parses cleanly with no userinfo" is not a safe
// acceptance test. An EMPTY endpoint is allowed - that is the AWS default,
// where the region alone selects the host.
func validateS3Endpoint(subject, endpoint string) error {
	if endpoint == "" {
		return nil
	}
	if strings.Contains(endpoint, "@") {
		return fmt.Errorf("%s: s3 endpoint must not contain credentials; supply the access key and secret in their own fields, and give the endpoint as scheme://host[:port]", subject)
	}
	if _, err := url.Parse(endpoint); err != nil {
		return fmt.Errorf("%s: s3 endpoint is not a valid URL: %w", subject, err)
	}
	return nil
}

// pathOnContainerRootFS is the seam the ephemeral-path warning goes through.
// The real implementation is per-OS (syscall.Stat on Linux, a "cannot tell"
// stub elsewhere), so a test on any host can drive both answers without
// needing a container or a real mount.
var pathOnContainerRootFS = pathIsOnContainerRootFS

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

// SyncStorageGate points the two storage connection mechanisms at whichever
// backend is currently configured: the watchdog watches the host path, or is
// stopped, and the s3 state is dropped when the backend is not s3.
//
// Both halves matter, because each mechanism can otherwise outlive the backend
// it describes. A gate left watching an abandoned path would keep refusing
// requests that no longer go there; an s3 state left reconnecting after a
// switch to a host path would report an outage forever, since the only thing
// that can clear it is a successful s3 call that will never happen.
//
// It is called at boot and from persistCoreStorageConfig, which is the single
// writer of these settings keys and therefore the only thing that can change
// the answer. Watch, Stop and Reset are all idempotent, so calling this on a
// config write that changed something else costs nothing.
func (s *AppState) SyncStorageGate() {
	cfg := s.LoadCoreStorageConfig()
	if (cfg.Backend == "path" || cfg.Backend == "local") && cfg.Path != "" {
		s.StorageGate.Watch(cfg.Path)
	} else {
		s.StorageGate.Stop()
	}
	if cfg.Backend != "s3" {
		s.StorageS3.Reset()
	}
}

// isHostPathBackend recognises the two spellings of the filesystem backend.
// "local" is the historical value; "path" is what the panel writes today. Both
// still appear in stored configs.
//
// It is NOT the only place they are matched, and an earlier version of this
// comment saying so was wrong: validateCoreStorageConfig, SyncStorageGate,
// TestConnection's ephemeral-path branch and health.go's storageComponent each
// switch on the literals directly. Anyone adding a third spelling has to visit
// all of them - this helper alone will not carry it.
func isHostPathBackend(backend string) bool {
	return backend == "path" || backend == "local"
}

// CountOnlineCores reports how many Core instances are currently heartbeating.
//
// It exists for the host-path backend, which stores files on ONE machine's
// filesystem. With a second Core online, half the reads miss and half the
// writes land where the other Core will never look for them - and nothing
// fails loudly, because each Core's own writes read back perfectly.
func (s *AppState) CountOnlineCores(ctx context.Context) (int, error) {
	ids, err := services.OnlineCoreIDs(ctx, s.Redis)
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

// hostPathMultiCoreMessage is shared by the save refusal and the panel warning
// so the operator is told the same thing in both places.
const hostPathMultiCoreMessage = "The filesystem backend stores files on one machine's disk, and %d Core instances are online. Each Core would serve only the files it wrote itself. Use the S3 backend, or mount a shared filesystem (NFS/SMB) at this path on every host. A Core that was killed rather than shut down cleanly can still be counted for up to 30 seconds."

// hostPathMultiCoreWarning returns the operator-facing warning for a config
// that is ALREADY saved as a host path on a deployment that has since grown
// past one Core, or "" when there is nothing to warn about.
//
// A second Core appearing after the fact does NOT auto-disable the backend.
// Silently repointing where files are stored is worse than a loud warning: the
// operator may be mid-deploy, and an automatic switch would strand every file
// written so far somewhere Core no longer looks.
//
// Takes the count rather than fetching it so the caller pays for one Redis
// round trip, and so this stays a pure function.
func hostPathMultiCoreWarning(cfg CoreStorageConfig, online int) string {
	if !isHostPathBackend(cfg.Backend) || online <= 1 {
		return ""
	}
	return fmt.Sprintf(hostPathMultiCoreMessage, online)
}

// WarnAboutHostPathAtBoot logs the warning above once at startup, so a Core
// joining a host-path deployment says so even if no admin opens the storage
// tab. Returns the message it logged (empty when there was nothing to warn
// about) so the behaviour is testable without capturing log output.
func (s *AppState) WarnAboutHostPathAtBoot(ctx context.Context) string {
	online, err := s.CountOnlineCores(ctx)
	if err != nil {
		log.Printf("core storage: could not count online Cores at boot: %v", err)
		return ""
	}
	warning := hostPathMultiCoreWarning(s.LoadCoreStorageConfig(), online)
	if warning != "" {
		log.Printf("core storage: WARNING: %s", warning)
	}
	return warning
}

// hostPathRefusal is returned by checkHostPathAllowed when a filesystem-backend
// config must not be persisted. It carries the HTTP status the settings form
// should answer with; a non-HTTP caller (the storage-migration config switch)
// uses only Error().
type hostPathRefusal struct {
	status  int
	message string
}

func (e *hostPathRefusal) Error() string { return e.message }

// checkHostPathAllowed refuses a filesystem-backend config while more than one
// Core is online, and returns nil otherwise. It is the single gate both the
// settings save and the storage-migration config switch go through, so neither
// can persist a host path the other would reject - the two used to disagree,
// and a migration to a host-path target sailed past the guard the save had.
//
// A count error is treated as a refusal rather than a pass. This is a rare,
// deliberate admin action, not a hot path, so "could not verify" is worth a
// retry; letting it through would silently split file storage across instances,
// which is the failure this whole check exists to prevent. Redis being
// unreachable is separately visible on the health page, so the operator is not
// left guessing about the cause.
func (s *AppState) checkHostPathAllowed(ctx context.Context, cfg CoreStorageConfig) error {
	if !isHostPathBackend(cfg.Backend) {
		return nil
	}
	online, err := s.CountOnlineCores(ctx)
	if err != nil {
		log.Printf("core storage: could not count online Cores: %v", err)
		return &hostPathRefusal{
			status:  http.StatusServiceUnavailable,
			message: "Could not verify how many Core instances are online, so the filesystem backend cannot be used right now. Check that Redis is reachable and try again.",
		}
	}
	// 0 and 1 are both "not more than one": a count of 0 means this Core's own
	// heartbeat has not landed yet, not that no Core is running.
	if online <= 1 {
		return nil
	}
	return &hostPathRefusal{
		status:  http.StatusConflict,
		message: fmt.Sprintf(hostPathMultiCoreMessage, online),
	}
}

// guardHostPathBackend adapts checkHostPathAllowed to the SaveConfig handler's
// (ok, status, message) shape. It gates the SAVE only; TestConnection is
// deliberately left open, because testing whether a path is reachable stays a
// useful and harmless thing to do on any number of Cores.
func (h *CoreStorageHandler) guardHostPathBackend(ctx context.Context, cfg CoreStorageConfig) (ok bool, status int, message string) {
	err := h.state.checkHostPathAllowed(ctx, cfg)
	if err == nil {
		return true, 0, ""
	}
	var refusal *hostPathRefusal
	if errors.As(err, &refusal) {
		return false, refusal.status, refusal.message
	}
	return false, http.StatusServiceUnavailable, err.Error()
}

// ProbeS3Connection performs one cheap, read-only call against the configured
// s3 backend. It is the probe storage.S3Resilience.StartProbe runs while the
// backend is reconnecting, and the only thing that lets an upload-only Core
// notice that the object store came back.
//
// It deliberately builds a RAW provider (nil gate, nil resilience). Going
// through the resilient wrapper would put the probe inside s3Retry, where it
// would pause and retry for the whole budget instead of returning a verdict.
//
// ListFiles, not the write/read/delete probe TestConnection uses: this runs
// every interval for as long as an outage lasts, so it must not generate
// writes, and the state it is clearing is about the transport rather than
// about permissions. A backend that answers a list but refuses a write is
// reachable, which is exactly what the reconnecting state asks about.
func (s *AppState) ProbeS3Connection(ctx context.Context) error {
	cfg := s.LoadCoreStorageConfig()
	if cfg.Backend != "s3" {
		return storage.ErrS3ProbeUnavailable
	}
	if err := validateCoreStorageConfig(cfg); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrS3ProbeUnavailable, err)
	}
	prov, err := newStorageProviderForConfig(cfg, "_probe", nil, nil)
	if err != nil {
		// A client that cannot be built says the config is wrong, not that the
		// connection failed. Reporting it as a verdict would clear a real
		// outage the moment somebody mistyped an endpoint.
		return fmt.Errorf("%w: %v", storage.ErrS3ProbeUnavailable, err)
	}
	_, err = prov.ListFiles(ctx, "")
	return err
}

// buildCoreStorageProvider returns a provider SCOPED to subPrefix, built
// strictly from the persisted Core file storage config. There is no
// legacy-directory fallback: the owner has confirmed there are no pre-
// existing DYLARIS installs anywhere, so every install starts with no local
// data to fall back to - a missing/invalid config is therefore a hard error,
// and callers must never proceed with a nil provider.
//
// A provider that still fails to CONSTRUCT despite a valid-looking config
// (e.g. a bad S3 endpoint rejected at client-construction time) is logged
// here, once, so that failure is never silent - callers only ever surface a
// generic "not configured" style response to the client either way.
func (s *AppState) buildCoreStorageProvider(subPrefix string) (storage.StorageProvider, error) {
	cfg := s.LoadCoreStorageConfig()
	if err := validateCoreStorageConfig(cfg); err != nil {
		return nil, err
	}
	prov, err := newStorageProviderForConfig(cfg, subPrefix, s.StorageGate, s.StorageS3)
	if err != nil {
		// An unreachable host path is not logged here. It is a per-REQUEST
		// build, so a wedged mount would write one line per request for as
		// long as the outage lasts, drowning the log at exactly the moment it
		// is being read. The gate's own transition is the signal for that
		// case; this line stays for the failures nothing else reports.
		if !errors.Is(err, storage.ErrBackendUnreachable) {
			log.Printf("core storage: failed to build %s provider: %v", subPrefix, err)
		}
		return nil, err
	}
	return prov, nil
}

// coreStorageUnavailableResponse writes a 503 telling the caller Core file
// storage must be configured (or is broken) before this endpoint can work.
// Every handler that resolves a provider per-request via
// buildCoreStorageProvider calls this on failure instead of proceeding with
// a nil provider.
//
// Deliberately NOT featureDisabledResponse/FeatureCoreStorage's exact shape
// (no X-Feature-Disabled header, a different "error" value): that response is
// reserved for RequireCoreStorageConfigured, the router-level gate wrapping
// EXACTLY the 4 WRITE routes, which fires before the request body is even
// parsed. This is the per-request backstop reached from inside a handler
// body - including READ handlers, which the gate never wraps - so keeping
// the two responses distinguishable preserves that route-coverage invariant
// instead of muddying it.
//
// err splits the two ways a per-request build fails, because they call for
// opposite actions from the operator. "Not configured" means go and configure
// it. UNREACHABLE means the config is fine and Core cannot see the storage
// right now, so there is nothing to fix in the settings form. Reporting the
// second as the first sends the operator to rewrite a working config; a plain
// 500 tells them nothing; and a 404 would be an outright lie, claiming the
// file is gone when the truth is that Core cannot see the mount. Both keep the
// shape this response already had, so the distinction from featureDisabledResponse
// described above is unaffected.
func coreStorageUnavailableResponse(w http.ResponseWriter, err error) {
	code := "core_storage_unavailable"
	msg := "Core file storage is not configured. Configure it under Settings -> Core file storage."
	if errors.Is(err, storage.ErrBackendUnreachable) {
		code = "core_storage_unreachable"
		msg = "Core file storage is UNREACHABLE: the configured path is not answering. The configuration itself is unchanged; check that the mount is still up on the Core host."
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   code,
		"message": msg,
	})
}

// newStorageProviderForConfig builds a StorageProvider strictly from cfg
// (assumed already validated), scoped to subPrefix. Used both by
// buildCoreStorageProvider (which loads the persisted config first) and by
// TestConnection (which validates a candidate config that may not be saved
// yet), so provider construction lives in exactly one place either way.
//
// gate is the watchdog for the LIVE configured host path, and it is passed in
// rather than read from AppState because not every caller is building against
// that path: the migration target and the connection test both hand this
// function a candidate config the gate knows nothing about. Those pass nil,
// which disables gating for that build. The gate never applies to s3.
//
// s3res is the same arrangement for the s3 backend, and the nil cases matter
// for a second reason there. The connection probe MUST fail fast: pausing a
// "test connection" click for up to ten minutes to re-confirm an endpoint the
// admin just typed wrong would be indefensible. The migration target passes nil
// because, like the gate, the live instance knows nothing about a candidate
// config. s3res never applies to a host path.
func newStorageProviderForConfig(cfg CoreStorageConfig, subPrefix string, gate *storage.Gate, s3res *storage.S3Resilience) (storage.StorageProvider, error) {
	if cfg.Backend == "s3" {
		prefix := subPrefix
		if cfg.S3Prefix != "" {
			prefix = cfg.S3Prefix + "/" + subPrefix
		}
		prov, err := storage.NewProvider("s3", "", map[string]string{
			storage.OptS3Endpoint:  cfg.S3Endpoint,
			storage.OptS3Bucket:    cfg.S3Bucket,
			storage.OptS3Region:    cfg.S3Region,
			storage.OptS3AccessKey: cfg.S3AccessKey,
			storage.OptS3SecretKey: cfg.S3SecretKey,
			storage.OptS3PathStyle: boolStr(cfg.S3PathStyle),
			storage.OptS3Prefix:    prefix,
		})
		if err != nil {
			return nil, err
		}
		if s3res == nil {
			return prov, nil
		}
		return storage.NewS3ResilientProvider(prov, s3res), nil
	}
	root := filepath.Join(cfg.Path, subPrefix)
	// BEFORE the MkdirAll, and that ordering is the whole point. This function
	// runs on every provider build and providers are built per request, so on
	// a mount that has stopped answering this MkdirAll is itself the call that
	// blocks - before any provider method is ever reached. Checking after it
	// would gate nothing that matters.
	if err := storage.GatedProviderBlocked(gate); err != nil {
		return nil, err
	}
	// Under the same concurrency bound as every provider method, for the same
	// reason: on a wedged mount this MkdirAll is a syscall that never returns,
	// and one per request is unbounded. MkdirAll's OWN error stays ignored as
	// before; only the gate giving up on it aborts the build. There is no
	// request context to pass here, and a caller waiting for a free slot is
	// waiting on a bound, not on the mount.
	if err := gate.Do(context.Background(), func() error {
		_ = os.MkdirAll(root, 0755)
		return nil
	}); err != nil {
		return nil, err
	}
	prov, err := storage.NewProvider("path", root, nil)
	if err != nil {
		return nil, err
	}
	return storage.NewGatedProvider(prov, gate), nil
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
// GetConfig GET /api/settings/core-storage - PANEL settings.read (RequireCap
// at the route). Alongside the stored config it answers the two questions the
// form needs about the host-path backend: may it be selected, and is the one
// already saved now unsafe.
func (h *CoreStorageHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := h.state.LoadCoreStorageConfig()
	cfg.S3SecretKey = "" // write-only; never emitted

	// The instance ids are NOT emitted, only the count. They are hostnames, and
	// the count alone is everything the form has to render.
	online, err := h.state.CountOnlineCores(r.Context())
	hostPathAllowed := true
	if err != nil {
		// A hint the UI could not compute is not a reason to grey out a valid
		// option. The save path is the enforcement point and refuses on this
		// same error, with a message that explains itself; the form stays
		// usable in the meantime.
		log.Printf("core storage: could not count online Cores for the settings form: %v", err)
	} else {
		hostPathAllowed = online <= 1
	}

	warning := hostPathMultiCoreWarning(cfg, online)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":         true,
		"settings":        cfg,
		"onlineCores":     online,
		"hostPathAllowed": hostPathAllowed,
		"hostPathWarning": warning,
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

	// After validation, before any write: a refused save must never leave a
	// half-applied config behind.
	if ok, status, msg := h.guardHostPathBackend(r.Context(), effective); !ok {
		sendJSONError(w, msg, status)
		return
	}

	// req.S3SecretKey, not effective.S3SecretKey: the merged config carries the
	// STORED secret whenever the request omitted the backend, so passing the
	// merged value would turn a secret-only rotation into a no-op that still
	// answers 200.
	if err := h.state.persistCoreStorageConfig(effective, req.S3SecretKey); err != nil {
		// The wrapped error carries the raw store error; the response names
		// only the settings key, since settings.write is a delegatable panel
		// capability and DB error text is not for it.
		log.Printf("core storage: save config: %v", err)
		msg := "Failed to save setting"
		var writeErr *coreStorageSettingWriteError
		if errors.As(err, &writeErr) {
			msg = "Failed to save setting: " + writeErr.Key
		}
		sendJSONError(w, msg, http.StatusInternalServerError)
		return
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
	respondWarn := func(ok bool, msg, warning string) {
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "ok": ok, "message": msg, "warning": warning})
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
	// nil gate: the candidate may name a completely different path from the
	// live one, so the live gate's verdict says nothing about it. An operator
	// who asks to test a path is also entitled to have it actually tested
	// rather than answered from a cached verdict.
	prov, err := newStorageProviderForConfig(effective, "_probe", nil, nil)
	if err != nil {
		respond(false, "Could not build provider: "+err.Error())
		return
	}
	ok, msg := probeStorageProvider(r.Context(), prov)
	if effective.Backend == "path" || effective.Backend == "local" {
		// newStorageProviderForConfig(effective, "_probe") MkdirAll'd this
		// exact directory; probeStorageProvider cleans up the object inside
		// it on every return path, so it is empty by now. os.Remove (never
		// RemoveAll) only succeeds on an empty dir, so this can't remove
		// anything else - best-effort, so a tested-then-rejected/never-saved
		// path doesn't keep an empty "_probe" dir behind forever.
		_ = os.Remove(filepath.Join(effective.Path, "_probe"))

		// The probe above passes on an UNMOUNTED path: a directory that only
		// exists in the container's own writable layer is perfectly writable,
		// readable and consistent, so every check this handler performs
		// succeeds while the data silently disappears on the next container
		// recreation. Reporting that as a plain green result is the trap.
		//
		// A warning, not a failure: an ephemeral path is a legitimate choice
		// for a throwaway dev instance, and this handler must not be the thing
		// that decides otherwise.
		if onRoot, determinable := pathOnContainerRootFS(effective.Path); determinable && onRoot {
			respondWarn(ok, msg, "This path is on the container's own filesystem, not a mounted volume, so everything written here is LOST when the container is recreated. The read/write test above still passes because that directory is genuinely writable. To keep the data, add a bind mount or volume for this path in your compose/stack file, or point the path at a directory inside a volume that is already mounted.")
			return
		}
	}
	respond(ok, msg)
}

// probeStorageProvider writes, reads back and deletes a uniquely-named probe
// object to verify the backend is reachable and read/write-consistent. The
// probe key is deleted on every path, including a write error, a read error,
// or a read-back/content mismatch, so a broken candidate config never leaves
// a stray object behind.
func probeStorageProvider(ctx context.Context, prov storage.StorageProvider) (ok bool, message string) {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	key := "probe-" + hex.EncodeToString(b) + ".txt"

	if err := prov.WriteFile(ctx, key, strings.NewReader(coreStorageProbePayload)); err != nil {
		// LocalProvider no longer leaves a partial object behind on a failed
		// write (it stages and renames), but the interface does not promise
		// that of a backend, and DeletePath is idempotent. Clean up on this
		// path too rather than trusting every implementation to be tidy.
		_ = prov.DeletePath(ctx, key)
		return false, "Write failed: " + err.Error()
	}
	rc, err := prov.GetFile(ctx, key)
	if err != nil {
		_ = prov.DeletePath(ctx, key)
		return false, "Read-back failed: " + err.Error()
	}
	got, readErr := io.ReadAll(rc)
	rc.Close()
	if readErr != nil {
		_ = prov.DeletePath(ctx, key)
		return false, "Read-back failed: " + readErr.Error()
	}
	if string(got) != coreStorageProbePayload {
		_ = prov.DeletePath(ctx, key)
		return false, "Read-back mismatch: storage backend is not consistent"
	}
	if err := prov.DeletePath(ctx, key); err != nil {
		return false, "Cleanup failed: " + err.Error()
	}
	return true, "Storage reachable: write, read and delete all succeeded."
}
