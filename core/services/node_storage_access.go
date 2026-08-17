package services

import (
	"context"
	"dylaris-core/models"
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Presigned-URL TTLs for node backup transfer. Two knobs because a BYON tenant's
// home uplink can be much slower than an operator DC node, so its presigned URL
// must stay valid long enough to finish a multi-GB transfer.
const (
	PresignTTLNodeKey        = "r2.presign_ttl_node_minutes" // operator nodes
	PresignTTLBYONKey        = "r2.presign_ttl_byon_minutes" // BYON tenant nodes
	DefaultPresignTTLNodeMin = 60                            // 1h
	DefaultPresignTTLBYONMin = 360                           // 6h
)

func presignTTL(st store.Store, isBYON bool) time.Duration {
	key, def := PresignTTLNodeKey, DefaultPresignTTLNodeMin
	if isBYON {
		key, def = PresignTTLBYONKey, DefaultPresignTTLBYONMin
	}
	mins := def
	if v, _ := st.GetSetting(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mins = n
		}
	}
	return time.Duration(mins) * time.Minute
}

// nodeResolvesProvider reports whether the NODE can act on a backup provider by
// itself. The list mirrors the switch in platform/node/backup_worker.go
// (uploadBackup) and backup_restore.go (downloadBackup) — nothing else exists
// there.
//
// Everything Core added later ("connection", "core-storage") is an INDIRECTION:
// the row names a target instead of describing one, and only Core can resolve
// it. Handing such a row to a node verbatim is what made every backup to a saved
// storage connection fail with "upload failed: unknown provider connection" —
// the panel offered the target, Core accepted it, and the failure surfaced two
// hops away with no hint that Core was supposed to resolve it first.
func nodeResolvesProvider(provider string) bool {
	switch provider {
	case "local", "shared", "node-local", "s3":
		return true
	}
	return false
}

// PrepareNodeStorage decides how a node receives storage access for a backup
// upload (op "put") or restore download (op "get").
//
//   - Operator node on a provider the node implements: returns the full storage
//     blob (with creds) + empty URL — exactly today's behavior.
//   - BYON tenant node on S3/R2: mints a presigned URL scoped to the single
//     object key + returns a CREDENTIAL-STRIPPED storage blob, so the tenant's
//     machine never receives the operator's bucket keys.
//   - ANY node on an indirection provider ("connection", "core-storage"): Core
//     resolves it here and presigns, because the node cannot. The presigned URL
//     also carries the resolved target's key prefix, which the node has no way
//     to reconstruct from the storage key alone.
//
// Fail-safe: if presigning fails for a BYON node on a plain s3 row, creds are
// STILL stripped and the URL is empty (the run fails rather than leaking
// credentials). For an indirection provider a presign failure is returned as an
// error instead: there is no fallback the node could take, so the caller must
// fail the run with a real reason rather than dispatch a doomed command.
func PrepareNodeStorage(ctx context.Context, st store.Store, storage *models.BackupStorage, node *models.Node, key, op string, deps backupstorage.Deps) (storageJSON []byte, presignedURL string, err error) {
	full, _ := json.Marshal(storage)
	if storage == nil {
		return full, "", nil
	}
	isBYON := node != nil && node.OwnerID != nil
	indirect := !nodeResolvesProvider(storage.Provider)

	// Node can resolve it and may hold the credentials: unchanged path.
	if !indirect && (!isBYON || storage.Provider != "s3") {
		return full, "", nil
	}

	stripped := *storage
	stripped.Config = json.RawMessage(`{}`)
	strippedJSON, _ := json.Marshal(&stripped)

	// fail reports a hard error for an indirection provider and the historical
	// silent fail-safe for a BYON s3 row.
	fail := func(cause error) ([]byte, string, error) {
		if indirect {
			return strippedJSON, "", fmt.Errorf("backup target %q (%s) cannot be reached by a node: %w", storage.Name, storage.Provider, cause)
		}
		return strippedJSON, "", nil
	}

	prov, err := backupstorage.Open(ctx, storage, deps)
	if err != nil {
		return fail(err)
	}
	ttl := presignTTL(st, isBYON)
	var url string
	if op == "get" {
		url, err = prov.DownloadURL(ctx, key, ttl)
	} else {
		url, err = prov.UploadURL(ctx, key, ttl)
	}
	if err != nil {
		return fail(err)
	}
	if url == "" {
		return fail(fmt.Errorf("the target did not return a pre-signed URL (only S3-compatible storage can)"))
	}
	return strippedJSON, url, nil
}
