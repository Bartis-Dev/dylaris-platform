package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/redis/go-redis/v9"
)

// BackupRunCommand is the payload the Core scheduler / handler pushes onto
// the node's Redis queue to start a single backup-run.
type BackupRunCommand struct {
	RunID           int             `json:"runId"`
	JobID           int             `json:"jobId"`
	ServerUUID      string          `json:"serverUuid"`
	SubServer       string          `json:"subServer"`
	IncludePatterns []string        `json:"includePatterns"`
	ExcludePatterns []string        `json:"excludePatterns"`
	StorageKey      string          `json:"storageKey"`
	Storage         json.RawMessage `json:"storage"`
	// PresignedPutURL, when set, is a pre-signed S3/R2 PUT URL the node uploads
	// to instead of using bucket credentials (BYON tenant nodes never receive
	// the operator's creds). Empty = use Storage creds (operator nodes).
	PresignedPutURL string `json:"presignedPutUrl"`
}

type storageInfo struct {
	ID       int             `json:"id"`
	Name     string          `json:"name"`
	Provider string          `json:"provider"`
	Config   json.RawMessage `json:"config"`
}

type localCfg struct {
	BasePath string `json:"basePath"`
}

type s3Cfg struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	ForcePathStyle  bool   `json:"forcePathStyle"`
}

// isBackupStoreEntry reports whether a walk-relative path is the archive store
// or something inside it. Such an entry never belongs in an archive.
//
// A whole-server job (sub_server NULL, which Core's validSubServer accepts as
// "the whole container") walks the server ROOT, and .dylaris-backups sits
// inside it: without this, every run archives every earlier run, so backup N
// carries backups 1..N-1 on top of the world. Worse, the node-local upload
// target is a file inside the very tree being walked, so a run can stream its
// own half-written archive into itself.
func isBackupStoreEntry(rel string) bool {
	return rel == backupDirName || strings.HasPrefix(rel, backupDirName+"/")
}

// RunBackup builds the archive and streams it directly to storage via an
// io.Pipe — no buffering in RAM. The tar+gzip writer runs in a goroutine
// that pushes bytes into the pipe; the storage uploader reads from the
// other end and pushes them out. For multi-GB worlds this keeps the
// node's working set under a few megabytes regardless of archive size.
func RunBackup(ctx context.Context, rdb *redis.Client, sm *StorageManager, cmd BackupRunCommand) {
	// At-least-once delivery: a redelivery while this run is still going would
	// archive the same tree twice into the same key. See backup_inflight.go.
	key := fmt.Sprintf("%d", cmd.RunID)
	if !backupsInFlight.enter(key) {
		log.Printf("backup_run: run %s is already running on this node, ignoring the redelivery", key)
		return
	}
	defer backupsInFlight.leave(key)

	started := time.Now()
	storage := storageInfo{}
	if err := json.Unmarshal(cmd.Storage, &storage); err != nil {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "invalid storage payload: "+err.Error(), 0)
		return
	}

	serverRoot := resolveServerRoot(sm, cmd.ServerUUID)
	rootDir := serverRoot
	if cmd.SubServer != "" {
		rootDir = filepath.Join(serverRoot, cmd.SubServer)
		// Core validates the name, but this is where it becomes a path: Join
		// cleans "..", it does not confine, so an unconfined name here would
		// archive whatever the node process can read - other tenants' servers
		// and the cached .node_secret included.
		if !withinRoot(serverRoot, rootDir) {
			reportBackup(ctx, rdb, cmd.RunID, "failed", "sub-server escapes the server directory", 0)
			return
		}
	}
	if _, err := os.Stat(rootDir); err != nil {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "source directory not found: "+rootDir, 0)
		return
	}

	// For node-local storage the destination is on the same disk we're
	// reading from — no pipe/uploader is needed. We still want to keep
	// the streaming tar+gzip path so RAM stays bounded for large worlds,
	// so we use the same io.Pipe + uploader plumbing and let
	// uploadBackup() switch on storage.Provider to do the right thing
	// (io.Copy to a hidden .dylaris-backups/<id>.tar.gz on the Node).
	pr, pw := io.Pipe()
	// counting writer wraps the pipe writer so we can report the archived
	// size without waiting for the upload to complete.
	counter := &countingWriter{}
	mw := io.MultiWriter(pw, counter)

	addedAny := false

	go func() {
		// The goroutine is the source side of the pipe. Any error we hit
		// has to propagate to the reader by closing the pipe with that
		// error so the uploader sees it and aborts cleanly.
		gw := gzip.NewWriter(mw)
		tw := tar.NewWriter(gw)

		walkErr := filepath.Walk(rootDir, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			rel, _ := filepath.Rel(rootDir, path)
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return nil
			}
			if isBackupStoreEntry(rel) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if matchAny(rel, cmd.ExcludePatterns) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if len(cmd.IncludePatterns) > 0 && !matchAny(rel, cmd.IncludePatterns) {
				return nil
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = rel
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if !info.IsDir() {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(tw, f)
				f.Close()
				if copyErr != nil {
					return copyErr
				}
			}
			addedAny = true
			return nil
		})

		// Order matters: close tar (flush remaining records) then gzip
		// (flush trailer) then the pipe writer so the uploader sees EOF.
		tarErr := tw.Close()
		gzErr := gw.Close()

		if walkErr != nil {
			pw.CloseWithError(walkErr)
			return
		}
		if tarErr != nil {
			pw.CloseWithError(tarErr)
			return
		}
		if gzErr != nil {
			pw.CloseWithError(gzErr)
			return
		}
		pw.Close()
	}()

	// Upload reads from the pipe. Returns once EOF (or pipe error) reached.
	// A BYON tenant node gets a pre-signed PUT URL and never sees bucket creds;
	// it stages the archive to a temp file first (PUT needs Content-Length).
	var upErr error
	if cmd.PresignedPutURL != "" {
		upErr = uploadBackupPresigned(ctx, cmd.PresignedPutURL, pr)
	} else {
		upErr = uploadBackup(ctx, sm, cmd.ServerUUID, storage, cmd.StorageKey, pr)
	}
	if upErr != nil {
		// Drain any remaining bytes so the writer goroutine doesn't block on
		// a full pipe; CloseWithError unblocks the writer immediately.
		pr.CloseWithError(upErr)
		reportBackup(ctx, rdb, cmd.RunID, "failed", "upload failed: "+upErr.Error(), 0)
		return
	}

	size := counter.Total()
	if !addedAny {
		// We still uploaded a 0-file archive; clean up storage and surface
		// the friendlier error so the UI doesn't show a zero-byte success.
		deleteBackup(ctx, sm, cmd.ServerUUID, storage, cmd.StorageKey)
		reportBackup(ctx, rdb, cmd.RunID, "failed", "no files matched include/exclude patterns", 0)
		return
	}

	reportBackup(ctx, rdb, cmd.RunID, "success", "", size)
	log.Printf("Backup %d streamed to %s/%s — %.2f MB in %v", cmd.RunID, storage.Provider, cmd.StorageKey, float64(size)/1024/1024, time.Since(started))
}

// countingWriter sums the byte count of an in-flight stream without
// buffering. Used so we can report the compressed archive size even though
// we never hold the full archive in memory.
type countingWriter struct{ n atomic.Int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n.Add(int64(len(p)))
	return len(p), nil
}

func (c *countingWriter) Total() int64 { return c.n.Load() }

// uploadBackup streams the body to whatever provider the storage config
// names. For local/shared that's a simple io.Copy to disk; for S3 we use
// the SDK manager.Uploader which transparently switches to multipart
// upload when the body exceeds the part-size threshold, so we never have
// to know the final archive size up front. For node-local the archive
// lands inside the server's own .dylaris-backups/ folder on this Node's
// disk — the storage key's tail filename is taken as the archive name and
// the directory is created on demand.
func uploadBackup(ctx context.Context, sm *StorageManager, serverUUID string, info storageInfo, key string, r io.Reader) error {
	switch info.Provider {
	case "local", "shared":
		// "shared" is the new UI label for the legacy "local" provider —
		// same Core-side filesystem path, just relabeled in the panel.
		var cfg localCfg
		if err := json.Unmarshal(info.Config, &cfg); err != nil {
			return fmt.Errorf("invalid local cfg: %w", err)
		}
		if cfg.BasePath == "" {
			return fmt.Errorf("local storage requires basePath")
		}
		full := filepath.Join(cfg.BasePath, filepath.Clean("/"+key))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, r)
		return err

	case "node-local":
		// Resolve <server-dir>/.dylaris-backups/<archive>. The Core-side
		// storage key includes a "backups/<serverUUID>/job-<jobID>/..."
		// prefix for display, but on-disk we collapse it to the leaf
		// filename — the directory is server-scoped already so the prefix
		// adds no information.
		dir := filepath.Join(resolveServerRoot(sm, serverUUID), backupDirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create node-local backup dir: %w", err)
		}
		archive := nodeLocalArchiveName(key)
		full := filepath.Join(dir, archive)
		f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, r)
		return err

	case "s3":
		client, bucket, err := buildS3Client(ctx, info.Config)
		if err != nil {
			return err
		}
		uploader := manager.NewUploader(client, func(u *manager.Uploader) {
			u.PartSize = 16 * 1024 * 1024 // 16 MiB per part — balances RAM vs. PUT count
			u.Concurrency = 3             // 3 in-flight parts, ~48 MiB peak window
		})
		_, err = uploader.Upload(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   r,
		})
		return err

	default:
		return fmt.Errorf("unknown provider %s", info.Provider)
	}
}

// presignedPutMaxSize is the S3/R2 single-PUT object limit. Larger BYON backups
// would need multipart-presigned (a follow-up); we fail clearly rather than
// silently truncate.
const presignedPutMaxSize = 5 * 1024 * 1024 * 1024 // 5 GiB

// uploadBackupPresigned uploads the archive to a pre-signed PUT URL. The PUT
// needs a Content-Length, but the archive is produced as an unbounded stream, so
// it is staged to a temp file first to learn the size. Used only for BYON tenant
// nodes (which must never receive bucket credentials).
func uploadBackupPresigned(ctx context.Context, url string, r io.Reader) error {
	tmp, err := os.CreateTemp("", "dylaris-backup-*.tar.gz")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	size, err := io.Copy(tmp, r)
	if err != nil {
		return fmt.Errorf("stage archive: %w", err)
	}
	if size > presignedPutMaxSize {
		return fmt.Errorf("archive %d bytes exceeds the 5 GiB single-upload limit for tenant nodes", size)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, tmp)
	if err != nil {
		return err
	}
	req.ContentLength = size
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("presigned put: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("presigned put status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// nodeLocalArchiveName collapses a storage key into its leaf filename so
// archives land directly under .dylaris-backups/ regardless of how Core
// chose to encode the key (job-N/, server-uuid/, etc.).
func nodeLocalArchiveName(key string) string {
	parts := strings.Split(filepath.ToSlash(key), "/")
	return parts[len(parts)-1]
}

// deleteBackup removes an archive that we already started writing but then
// decided to abort (e.g. empty include/exclude result). Best-effort —
// surfaces nothing back to the caller because the original error already
// covers the user-visible outcome.
func deleteBackup(ctx context.Context, sm *StorageManager, serverUUID string, info storageInfo, key string) {
	switch info.Provider {
	case "local", "shared":
		var cfg localCfg
		if json.Unmarshal(info.Config, &cfg) != nil || cfg.BasePath == "" {
			return
		}
		os.Remove(filepath.Join(cfg.BasePath, filepath.Clean("/"+key)))
	case "node-local":
		archive := nodeLocalArchiveName(key)
		os.Remove(filepath.Join(resolveServerRoot(sm, serverUUID), backupDirName, archive))
	case "s3":
		client, bucket, err := buildS3Client(ctx, info.Config)
		if err != nil {
			return
		}
		client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
	}
}

// buildS3Client centralises the SDK setup so the streaming upload and the
// best-effort delete share the same credential / endpoint resolution.
func buildS3Client(ctx context.Context, raw json.RawMessage) (*s3.Client, string, error) {
	var cfg s3Cfg
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, "", fmt.Errorf("invalid s3 cfg: %w", err)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, "", err
	}
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return client, cfg.Bucket, nil
}

// matchAny returns true when `path` (slash-separated, relative to the
// archive root) matches any of the glob patterns. Patterns ending in /**
// match everything below a directory; otherwise filepath.Match semantics.
func matchAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/**") {
			if strings.HasPrefix(path, strings.TrimSuffix(p, "/**")+"/") || path == strings.TrimSuffix(p, "/**") {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		// Also match against any path component so "*.log" hits nested files.
		base := filepath.Base(path)
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
	}
	return false
}

// reportBackup publishes the run result so Core can update the DB row.
// The Core listens on `dylaris:backup:results`.
func reportBackup(ctx context.Context, rdb *redis.Client, runID int, status, errMsg string, size int64) {
	payload := map[string]interface{}{
		"runId":     runID,
		"status":    status,
		"error":     errMsg,
		"sizeBytes": size,
		"timestamp": time.Now().Unix(),
	}
	data, _ := json.Marshal(payload)
	if err := rdb.Publish(ctx, "dylaris:backup:results", data).Err(); err != nil {
		log.Printf("backup result publish failed: %v", err)
	}
}

// resolveServerRoot resolves the sub-server root via the node's
// StorageManager. Falls back to the legacy default when sm is nil so
// development/testing can still run without multi-storage.
func resolveServerRoot(sm *StorageManager, uuid string) string {
	if sm == nil {
		return filepath.Join("./dylaris_data/servers", uuid)
	}
	return sm.GetServerDir(uuid)
}
