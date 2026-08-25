package main

import (
	"log"
	"strings"

	"dylaris-core/config"
)

// warnDNSMovedToHub reports a DNS_* variable that no longer does anything here.
//
// Core used to write the edge and beam-relay A records. It does not any more:
// every name that subsystem managed is a gateway name, so it lives in the
// gateway Hub, which is present in every gateway deployment and is the only
// component that can also do it when there is no platform at all.
//
// This is a log line rather than a silent removal, and rather than a fatal.
// Silent would leave an operator whose records stopped updating with nothing to
// search for; fatal would refuse to boot over a variable that is now merely
// surplus. Naming the destination is the whole value of the message.
func warnDNSMovedToHub(cfg *config.Config) {
	var set []string
	for name, value := range map[string]string{
		"DNS_UPDATER_ENABLED": boolText(cfg.DNSUpdaterEnabled),
		"DNS_PROVIDER":        cfg.DNSProvider,
		"DNS_API_TOKEN":       redactedIfSet(cfg.DNSAPIToken),
		"DNS_ZONE":            cfg.DNSZone,
		"DNS_ZONES":           cfg.DNSZones,
	} {
		if strings.TrimSpace(value) != "" {
			set = append(set, name)
		}
	}
	if len(set) == 0 {
		return
	}
	log.Printf("config: %s set but no longer read here - DNS records are written by the gateway Hub "+
		"(its admin UI, DNS page, or the same DNS_* variables on the hub service). Remove them from Core.",
		strings.Join(set, ", "))
}

// boolText renders only a true flag as present, so DNS_UPDATER_ENABLED=false
// does not produce a warning about a variable the operator already turned off.
func boolText(b bool) string {
	if b {
		return "true"
	}
	return ""
}

// redactedIfSet reports presence without putting the credential in the log.
func redactedIfSet(v string) string {
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return "set"
}
