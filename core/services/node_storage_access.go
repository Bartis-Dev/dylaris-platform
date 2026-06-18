package services

import (
	"context"
	"dylaris-core/models"
	backupstorage "dylaris-core/storage/backup"
	"dylaris-core/store"
	"encoding/json"
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

// PrepareNodeStorage decides how a node receives storage access for a backup
// upload (op "put") or restore download (op "get").
//
//   - Operator node (or non-S3 storage): returns the full storage blob (with
//     creds) + empty URL — exactly today's behavior.
//   - BYON tenant node on S3/R2: mints a presigned URL scoped to the single
//     object key + returns a CREDENTIAL-STRIPPED storage blob, so the tenant's
//     machine never receives the operator's bucket keys.
//
// Fail-safe: if presigning fails for a BYON node, creds are STILL stripped and
// the URL is empty (the run fails rather than leaking credentials).
func PrepareNodeStorage(ctx context.Context, st store.Store, storage *models.BackupStorage, node *models.Node, key, op string) (storageJSON []byte, presignedURL string) {
	full, _ := json.Marshal(storage)
	isBYON := node != nil && node.OwnerID != nil
	if !isBYON || storage == nil || storage.Provider != "s3" {
		return full, ""
	}

	stripped := *storage
	stripped.Config = json.RawMessage(`{}`)
	strippedJSON, _ := json.Marshal(&stripped)

	prov, err := backupstorage.Open(ctx, storage, backupstorage.Deps{})
	if err != nil {
		return strippedJSON, ""
	}
	ttl := presignTTL(st, true)
	var url string
	if op == "get" {
		url, _ = prov.DownloadURL(ctx, key, ttl)
	} else {
		url, _ = prov.UploadURL(ctx, key, ttl)
	}
	return strippedJSON, url
}
