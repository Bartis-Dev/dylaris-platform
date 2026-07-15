package services

import (
	"context"
	"log"
	"time"
)

// setupStatusReader is the slice of the Store interface this loop needs. Local
// to avoid a services->store import cycle.
type setupStatusReader interface {
	CountAdmins() (int, error)
	CountUsers() (int, error)
}

// StartSetupRecoveryLoop runs a background goroutine that logs a secret-free
// setup hint every 30s as long as the platform has no admin: fresh install ->
// "open <url>/setup"; no admin but users exist -> "set ADMIN_SECRET and restart
// Core, then open <url>/setup". It never generates or logs any token. Returns
// immediately; the goroutine stops when ctx is cancelled.
func StartSetupRecoveryLoop(ctx context.Context, store setupStatusReader, frontendURL string) {
	go func() {
		printSetupHint(store, frontendURL)
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				printSetupHint(store, frontendURL)
			}
		}
	}()
}

func printSetupHint(store setupStatusReader, frontendURL string) {
	adminCount, _ := store.CountAdmins()
	if adminCount > 0 {
		return
	}
	url := frontendURL
	if url == "" {
		url = "<host-not-configured>"
	}
	userCount, _ := store.CountUsers()
	if userCount == 0 {
		log.Printf("[SETUP] Fresh install - open %s/setup to create the first admin", url)
		return
	}
	log.Printf("[SETUP] No admin present. Set ADMIN_SECRET in Core's environment and restart, then open %s/setup to create a new admin.", url)
}
