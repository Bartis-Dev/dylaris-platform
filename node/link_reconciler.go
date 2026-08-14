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
		want := linkWanted(getRoutingMode(), secret, proof, linkImage)
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

// linkWanted reports whether this node should run its Link sidecar. Link carries
// gateway-routed traffic, so it is needed whenever routing is "gateway" OR "both"
// (the mixed mode where some servers still route by ip:port) - i.e. anything but
// pure "ip_port" - provided Core has delivered the Link creds and a Link image is
// configured. Gating on "gateway" alone left a domain route created in "both"
// silently dead: the Link never came up, so its route was never published.
func linkWanted(mode, secret, proof, image string) bool {
	return mode != "ip_port" && secret != "" && proof != "" && image != ""
}
