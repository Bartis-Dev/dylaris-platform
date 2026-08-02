package main

import (
	"context"
	"log"
	"time"
)

// startLinkReconciler manages the node's own Link sidecar. It (re)spawns Link when
// this node is gateway-routed, self-manages Link, and has its Core-delivered creds;
// it stops Link when those no longer hold. No-op unless NODE_MANAGES_LINK (creds are
// only delivered on the ACL path). Runs on a 30s tick so late-arriving creds / a
// routing-mode flip / a cred rotation are picked up without a restart.
func startLinkReconciler(ctx context.Context, dm *DockerManager) {
	if !nodeManagesLink {
		return
	}
	var last string // signature of the last-applied spawn; "" = not running
	reconcile := func() {
		secret, proof := getLinkCreds()
		want := getRoutingMode() == "gateway" && secret != "" && proof != "" && linkImage != ""
		sig := nodeID + "|" + secret + "|" + proof + "|" + linkImage
		if want {
			if sig != last {
				if err := dm.EnsureLinkContainer(linkImage, nodeID, secret, proof); err != nil {
					log.Printf("link: failed to ensure Link sidecar: %v", err)
					return
				}
				log.Println("link: Link sidecar (re)started")
				last = sig
			}
		} else if last != "" {
			dm.StopLinkContainer()
			log.Println("link: Link sidecar stopped")
			last = ""
		}
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
