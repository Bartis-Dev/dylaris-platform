package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
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

// RunBackup builds the archive, uploads it to storage and reports the
// outcome back to Core via Redis pub/sub on the result channel. The node
// process is expected to call this in its command dispatch loop.
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

	// Build the tar.gz in memory. For very large servers this should
	// stream to a pipe → uploader; we keep it simple in v1 and grow as
	// needed.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	addedAny := false
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
		// Includes are positive filters — when set, only matching paths go
		// in. Empty include list means "everything not excluded".
		if len(cmd.IncludePatterns) > 0 && !matchAny(rel, cmd.IncludePatterns) {
			// Directories still need to be walked in case a descendant matches.
			if info.IsDir() {
				return nil
			}
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
	tw.Close()
	gw.Close()

	if err != nil {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "archive build failed: "+err.Error(), 0)
		return
	}
	if !addedAny {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "no files matched include/exclude patterns", 0)
		return
	}

	size := int64(buf.Len())
	log.Printf("Backup %d archive built: %d bytes (%.2f MB) in %v", cmd.RunID, size, float64(size)/1024/1024, time.Since(started))

	// Upload according to the storage provider.
	if err := uploadBackup(ctx, storage, cmd.StorageKey, &buf); err != nil {
		reportBackup(ctx, rdb, cmd.RunID, "failed", "upload failed: "+err.Error(), 0)
		return
	}

	reportBackup(ctx, rdb, cmd.RunID, "success", "", size)
	log.Printf("Backup %d uploaded to %s/%s", cmd.RunID, storage.Provider, cmd.StorageKey)
}

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
		var cfg s3Cfg
		if err := json.Unmarshal(info.Config, &cfg); err != nil {
			return fmt.Errorf("invalid s3 cfg: %w", err)
		}
		if cfg.Region == "" {
			cfg.Region = "us-east-1"
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(cfg.Region),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")),
		)
		if err != nil {
			return err
		}
		client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if cfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(cfg.Endpoint)
			}
			o.UsePathStyle = cfg.ForcePathStyle
		})
		_, err = client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(cfg.Bucket),
			Key:    aws.String(key),
			Body:   r,
		})
		return err
	default:
		return fmt.Errorf("unknown provider %s", info.Provider)
	}
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
