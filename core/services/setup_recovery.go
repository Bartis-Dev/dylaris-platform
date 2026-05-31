package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"
)

// settingsRW is the slice of the Store interface this service needs. Local
// to break the cycle (services package cannot import handlers/store directly
// without circular references at some build configurations).
type settingsRW interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
	CountAdmins() (int, error)
	CountUsers() (int, error)
}

// StartSetupRecoveryLoop runs a background goroutine that prints either the
// Fresh-Install hint or the Lost-Admin recovery token + URL every 30s as
// long as the platform has no admin. It returns immediately; the goroutine
// stops when ctx is cancelled.
//
// Logs go to the default log package so they end up in whichever logger the
// Core writes to (stdout in dev, structured log file in deploy).
func StartSetupRecoveryLoop(ctx context.Context, store settingsRW, frontendURL string) {
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

func printSetupHint(store settingsRW, frontendURL string) {
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
		log.Printf("[SETUP] Fresh install — open %s/setup to create the first admin", url)
		return
	}
	token, _ := store.GetSetting("setup_recovery_token")
	if token == "" {
		token = genHexToken(32)
		if err := store.SetSetting("setup_recovery_token", token); err != nil {
			log.Printf("[SETUP] failed to persist recovery token: %v", err)
			return
		}
	}
	log.Printf("[SETUP] No admin in DB. Recovery: %s/setup\n          Token: %s", url, token)
}

func genHexToken(byteLen int) string {
	b := make([]byte, byteLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
