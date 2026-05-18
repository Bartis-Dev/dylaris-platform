package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// RunBackup builds the archive and streams it directly to storage via an
// io.Pipe — no buffering in RAM. The tar+gzip writer runs in a goroutine
// that pushes bytes into the pipe; the storage uploader reads from the
// other end and pushes them out. For multi-GB worlds this keeps the
// node's working set under a few megabytes regardless of archive size.
func RunBackup(ctx context.Context, rdb *redis.Client, sm *StorageManager, cmd BackupRunCommand) {
	started := time.Now()
	storage := storageInfo{}
	if err := json.Unmarshal(cmd.Storage, &storage); err != nil {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "invalid storage payload: "+err.Error(), 0)
		return
	}

	rootDir := resolveServerRoot(sm, cmd.ServerUUID)
	if cmd.SubServer != "" {
		rootDir = filepath.Join(rootDir, cmd.SubServer)
	}
	if _, err := os.Stat(rootDir); err != nil {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "source directory not found: "+rootDir, 0)
		return
	}

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
	if err := uploadBackup(ctx, storage, cmd.StorageKey, pr); err != nil {
		// Drain any remaining bytes so the writer goroutine doesn't block on
		// a full pipe; CloseWithError unblocks the writer immediately.
		pr.CloseWithError(err)
		reportBackup(ctx, rdb, cmd.RunID, "failed", "upload failed: "+err.Error(), 0)
		return
	}

	size := counter.Total()
	if !addedAny {
		// We still uploaded a 0-file archive; clean up storage and surface
		// the friendlier error so the UI doesn't show a zero-byte success.
		deleteBackup(ctx, storage, cmd.StorageKey)
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
// names. For local that's a simple io.Copy to disk; for S3 we use the
// SDK manager.Uploader which transparently switches to multipart upload
// when the body exceeds the part-size threshold, so we never have to
// know the final archive size up front.
func uploadBackup(ctx context.Context, info storageInfo, key string, r io.Reader) error {
	switch info.Provider {
	case "local":
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

// deleteBackup removes an archive that we already started writing but then
// decided to abort (e.g. empty include/exclude result). Best-effort —
// surfaces nothing back to the caller because the original error already
// covers the user-visible outcome.
func deleteBackup(ctx context.Context, info storageInfo, key string) {
	switch info.Provider {
	case "local":
		var cfg localCfg
		if json.Unmarshal(info.Config, &cfg) != nil || cfg.BasePath == "" {
			return
		}
		os.Remove(filepath.Join(cfg.BasePath, filepath.Clean("/"+key)))
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
