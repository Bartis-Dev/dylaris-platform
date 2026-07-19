package storagemigrate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"dylaris-core/storage"
	"dylaris-core/storage/backup"
	"dylaris-core/storage/modpack"
)

// Stable data-set identifiers. The three StorageProvider-backed ones reuse
// the Core file storage sub-prefix names so an operator sees one vocabulary
// across the config screen and the migration screen.
const (
	DataSetLibrary       = "library"
	DataSetAttachments   = "ticket-attachments"
	DataSetTicketBackups = "ticket-backups"
	DataSetModpacks      = "modpacks"

	// ServerBackupsIDPrefix namespaces per-backup_storages-row data sets,
	// because backups are multi-storage by design (backup_storages rows plus
	// BackupJob.StorageID). One global id would be strictly less expressive.
	ServerBackupsIDPrefix = "server-backups:"
)

// ServerBackupsDataSetID builds the id for one backup_storages row.
func ServerBackupsDataSetID(storageID int) string {
	return ServerBackupsIDPrefix + strconv.Itoa(storageID)
}

// ParseServerBackupsDataSetID reverses ServerBackupsDataSetID. The bool is
// false for any other data set id, and for a malformed or negative number.
func ParseServerBackupsDataSetID(id string) (int, bool) {
	if !strings.HasPrefix(id, ServerBackupsIDPrefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, ServerBackupsIDPrefix))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// ChecksumHinter is optionally implemented by a DataSet that carries
// PRE-EXISTING, third-party-supplied checksums for some of its keys. The
// manifest never stores these - its checksum column is always the freshly
// computed SHA-256 - but the capture pass logs a warning when a hint
// disagrees with what it just read, as an opportunistic integrity signal
// about data that predates this engine. A disagreement never fails a job.
type ChecksumHinter interface {
	ChecksumHint(key string) (algo, sum string, ok bool)
}

// ---------- StorageProvider-backed data sets (library, ticket-attachments, ticket-backups) ----------

type providerDataSet struct {
	id    string
	label string
	prov  storage.StorageProvider
}

// NewProviderDataSet adapts a Core file storage provider that the caller has
// already scoped to a sub-prefix, so every key here is provider-relative.
func NewProviderDataSet(id, label string, prov storage.StorageProvider) DataSet {
	return &providerDataSet{id: id, label: label, prov: prov}
}

func (d *providerDataSet) ID() string    { return d.id }
func (d *providerDataSet) Label() string { return d.label }

// List walks the WHOLE key space. StorageProvider.ListFiles is one directory
// level only, so the recursion is mandatory - see storage.WalkProvider.
func (d *providerDataSet) List(_ context.Context) ([]ObjectRef, error) {
	files, err := storage.WalkProvider(d.prov, "")
	if err != nil {
		return nil, fmt.Errorf("storagemigrate: list %s: %w", d.id, err)
	}
	out := make([]ObjectRef, 0, len(files))
	for _, f := range files {
		out = append(out, ObjectRef{Key: f.Key, Size: f.Size})
	}
	return out, nil
}

// Open relies on the providers' existing discrimination: LocalProvider
// returns os.Open's error and S3Provider.GetFile normalizes a genuine
// NoSuchKey/NotFound to an fs.ErrNotExist-comparable error while leaving any
// other backend failure unwrapped.
func (d *providerDataSet) Open(_ context.Context, key string) (io.ReadCloser, error) {
	return d.prov.GetFile(key)
}

func (d *providerDataSet) Write(_ context.Context, key string, r io.Reader, _ int64) error {
	return d.prov.WriteFile(key, r)
}

func (d *providerDataSet) Delete(_ context.Context, key string) error {
	return d.prov.DeletePath(key)
}

// ---------- backup.Storage-backed data set (server-backups:<id>) ----------

type backupDataSet struct {
	id    string
	label string
	st    backup.Storage
}

// NewBackupDataSet adapts one backup_storages row.
func NewBackupDataSet(id, label string, st backup.Storage) DataSet {
	return &backupDataSet{id: id, label: label, st: st}
}

func (d *backupDataSet) ID() string    { return d.id }
func (d *backupDataSet) Label() string { return d.label }

// List does NOT walk: backup.Storage.List is already recursive on both
// LocalStorage (filepath.Walk) and S3Storage (a ListObjectsV2 paginator), and
// the core-storage adapter recurses internally. Walking a second time would
// double-enumerate.
func (d *backupDataSet) List(ctx context.Context) ([]ObjectRef, error) {
	objs, err := d.st.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("storagemigrate: list %s: %w", d.id, err)
	}
	out := make([]ObjectRef, 0, len(objs))
	for _, o := range objs {
		out = append(out, ObjectRef{Key: o.Key, Size: o.Size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Open normalizes a missing key. This wrapper is REQUIRED, not defensive:
// backup.S3Storage.Get returns the raw smithy error for NoSuchKey (only its
// Delete special-cases it), so without this the copy loop would treat every
// legitimately-absent target object as a hard failure and never copy anything.
func (d *backupDataSet) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := d.st.Get(ctx, key)
	if err != nil {
		return nil, normalizeBackupNotFound(key, err)
	}
	return rc, nil
}

func (d *backupDataSet) Write(ctx context.Context, key string, r io.Reader, size int64) error {
	return d.st.Put(ctx, key, r, size)
}

func (d *backupDataSet) Delete(ctx context.Context, key string) error {
	return d.st.Delete(ctx, key)
}

// notFoundCodes are the two S3 API error codes that mean "this object is not
// here", matching the discrimination storage/s3provider.go and
// storage/modpack/s3.go already make.
var notFoundCodes = []string{"NoSuchKey", "NotFound"}

// normalizeBackupNotFound converts a backend's "object missing" error into
// one that is errors.Is(err, fs.ErrNotExist)-comparable, and leaves EVERY
// other error alone. Getting this wrong in either direction is dangerous: a
// throttle mistaken for "missing" makes the copy loop overwrite live data,
// and a "missing" mistaken for a failure makes a fresh migration impossible.
func normalizeBackupNotFound(key string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return err
	}
	msg := err.Error()
	for _, code := range notFoundCodes {
		if strings.Contains(msg, code) {
			return fmt.Errorf("backup get %s: %v: %w", key, err, fs.ErrNotExist)
		}
	}
	return err
}

// ---------- modpack data set ----------

// ModpackKeySource enumerates the modpacks key space from the DATABASE.
// ModpackStorageProvider has no List, and adding one is meaningless for the
// local backend (which mirrors across N paths, so "the" key space is
// ambiguous), so the union of the three storage-key columns is the only
// possible source.
type ModpackKeySource interface {
	ListModpackStorageKeys() ([]string, error)
	ListModversionSHA512ByStorageKey() (map[string]string, error)
}

type modpackDataSet struct {
	label string
	prov  modpack.ModpackStorageProvider
	keys  ModpackKeySource
}

// NewModpackDataSet adapts the modpack provider plus its DB key source.
func NewModpackDataSet(label string, prov modpack.ModpackStorageProvider, keys ModpackKeySource) DataSet {
	return &modpackDataSet{label: label, prov: prov, keys: keys}
}

func (d *modpackDataSet) ID() string    { return DataSetModpacks }
func (d *modpackDataSet) Label() string { return d.label }

// List enumerates DB-referenced keys and sizes them via Stat. Blank keys and
// keys with no object behind them are skipped.
//
// CONSEQUENCE, surfaced in the panel: verification is authoritative for
// REFERENCED objects only. An orphan in storage that no DB row points at is
// invisible to both the manifest and the "extra" check. That is a genuine
// limitation of the current schema, not a shortcut.
func (d *modpackDataSet) List(_ context.Context) ([]ObjectRef, error) {
	keys, err := d.keys.ListModpackStorageKeys()
	if err != nil {
		return nil, fmt.Errorf("storagemigrate: list %s: %w", DataSetModpacks, err)
	}
	seen := map[string]bool{}
	out := []ObjectRef{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		size, exists, err := d.prov.Stat(k)
		if err != nil {
			return nil, fmt.Errorf("storagemigrate: stat %s: %w", k, err)
		}
		if !exists {
			continue
		}
		out = append(out, ObjectRef{Key: k, Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Open buffers, because ModpackStorageProvider is []byte-shaped end to end.
// No regression: the existing s3 modpack backend already does io.ReadAll on
// Get. Reshaping that interface to stream is explicitly out of scope.
func (d *modpackDataSet) Open(_ context.Context, key string) (io.ReadCloser, error) {
	data, err := d.prov.Get(key)
	if err != nil {
		if errors.Is(err, modpack.ErrNotFound) {
			return nil, fmt.Errorf("modpack get %s: %w", key, fs.ErrNotExist)
		}
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (d *modpackDataSet) Write(_ context.Context, key string, r io.Reader, _ int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("storagemigrate: buffer %s: %w", key, err)
	}
	return d.prov.Put(key, data)
}

func (d *modpackDataSet) Delete(_ context.Context, key string) error {
	return d.prov.Delete(key)
}

// ChecksumHint returns the Modrinth/Solder-supplied SHA-512 already recorded
// for a modversion object, when there is one. Opportunistic only - see
// ChecksumHinter. A lookup failure is reported as "no hint" rather than
// failing the capture, because this signal is never load-bearing.
func (d *modpackDataSet) ChecksumHint(key string) (string, string, bool) {
	hints, err := d.keys.ListModversionSHA512ByStorageKey()
	if err != nil || hints == nil {
		return "", "", false
	}
	sum, ok := hints[key]
	if !ok || sum == "" {
		return "", "", false
	}
	return "sha512", sum, true
}

var _ ChecksumHinter = (*modpackDataSet)(nil)
