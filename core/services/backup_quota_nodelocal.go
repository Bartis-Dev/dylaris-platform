package services

import (
	"strconv"
	"time"

	nodegrpc "dylaris-core/grpc"
	"dylaris-core/models"
	"dylaris-core/store"

	pb "dylaris-proto/node"

	"github.com/google/uuid"
)

// Defaults for the node-local backup quota. They live here, not next to the
// settings struct, because BOTH the settings handler that renders them and the
// check that enforces them have to agree: the panel shows "1.2 GB / 10.0 GB"
// off the same number the refusal uses, and an install that never saved the
// form has no row at all.
const (
	DefaultBackupMode           = "shared"
	DefaultBackupQuotaPerServer = 10 // GB, 0 = unlimited
)

// backupUsageRPCTimeout matches the one BackupUsage already uses for the
// panel's usage card - same call, same node, no reason to differ.
const backupUsageRPCTimeout = 5 * time.Second

// NodeLocalBackupQuotaExceeded reports whether a server's node-local backup
// folder is at or over the configured per-server cap.
//
// The cap had NO consumer anywhere in the tree: `backup.quota_per_server_gb`
// was written by the settings form and read straight back into it, and nothing
// else ever looked at it. The Settings copy told the operator "Enforcement is
// application-level - Core checks current usage before approving a new run",
// and the server Overview drew a usage bar with a Free figure off the same
// number. MEASURED on the testbed: cap set to 1 GB with 1.0 GB already stored,
// Run Now was accepted, and the card then read 1.2 GB / 1.0 GB.
//
// Both producers of a run have to take this, or it is the same defect one
// layer down: startBackupRun for the manual/API path and the scheduler for
// cron.
//
// It FAILS OPEN when usage cannot be established (no registry, node not
// connected, RPC error, empty response). Refusing a backup because we could
// not measure would turn a brief node blip into a skipped backup, and a backup
// is the safety net - while a node we cannot reach cannot run the backup
// anyway, so nothing is actually let through.
func NodeLocalBackupQuotaExceeded(st store.Store, reg *nodegrpc.Registry, srv *models.Server) (exceeded bool, usedBytes, quotaBytes int64) {
	return nodeLocalBackupQuotaExceeded(st, srv, func() (int64, bool) {
		if reg == nil || srv == nil {
			return 0, false
		}
		resp, err := reg.SendRequest(srv.NodeID, &pb.NodeMessage{
			RequestId:  uuid.NewString(),
			ServerUuid: srv.UUID,
			Payload:    &pb.NodeMessage_BackupUsageReq{BackupUsageReq: &pb.BackupUsageReq{}},
		}, backupUsageRPCTimeout)
		if err != nil {
			return 0, false
		}
		usage := resp.GetBackupUsageResp()
		if usage == nil {
			return 0, false
		}
		return usage.UsedBytes, true
	})
}

// nodeLocalBackupQuotaExceeded is the decision itself, with the node round-trip
// behind `usage` so the boundary case (used == quota refuses, one byte under
// does not) is testable without a live gRPC mesh. usage returns ok=false when
// the folder size could not be established.
func nodeLocalBackupQuotaExceeded(st store.Store, srv *models.Server, usage func() (int64, bool)) (exceeded bool, usedBytes, quotaBytes int64) {
	if srv == nil {
		return false, 0, 0
	}

	mode, _ := st.GetSetting("backup.mode")
	if mode == "" {
		mode = DefaultBackupMode
	}
	// The cap is documented as node-local only: it counts the hidden
	// .dylaris-backups/ folder inside the server directory. s3/shared archives
	// are bounded by the tenant's R2 quota instead (R2QuotaExceeded).
	if mode != "node-local" {
		return false, 0, 0
	}
	// "Folded into the server's main disk quota" - a different budget, not this
	// one. Enforcing here too would charge the same bytes twice.
	if v, _ := st.GetSetting("backup.share_quota_with_server"); v == "true" {
		return false, 0, 0
	}

	quotaGB := int64(DefaultBackupQuotaPerServer)
	if v, _ := st.GetSetting("backup.quota_per_server_gb"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			// A malformed row must not silently become "unlimited". Fall back to
			// the same default the settings GET renders.
			n = DefaultBackupQuotaPerServer
		}
		quotaGB = n
	}
	if quotaGB <= 0 {
		return false, 0, 0
	}
	quotaBytes = quotaGB * 1024 * 1024 * 1024

	used, ok := usage()
	if !ok {
		return false, 0, quotaBytes
	}
	return used >= quotaBytes, used, quotaBytes
}
