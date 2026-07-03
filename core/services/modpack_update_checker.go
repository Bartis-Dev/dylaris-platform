package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"dylaris-core/pkg/leader"
	"dylaris-core/store"
)

// modpackUpdateChunkSize bounds hashes per Modrinth call, well under the shared
// 300 req/min limit given the hourly cadence + per-row staleness gate.
const modpackUpdateChunkSize = 100

// ModpackUpdateChecker is a leader-gated background worker that batch-checks
// Modrinth-linked modversions for a newer version and caches the result on each
// row (modrinth_latest_version_id + modrinth_last_checked). It reads only public
// Modrinth metadata (no PAT) and goes inert when the modpacks feature is off, so
// an operator disabling the feature pauses it non-destructively.
type ModpackUpdateChecker struct {
	store  store.Store
	flags  *FeatureFlags
	leader leader.Election
	tick   time.Duration
}

func NewModpackUpdateChecker(s store.Store, flags *FeatureFlags) *ModpackUpdateChecker {
	return &ModpackUpdateChecker{store: s, flags: flags, tick: time.Hour}
}

func (c *ModpackUpdateChecker) SetLeader(l leader.Election) { c.leader = l }

func (c *ModpackUpdateChecker) Start(ctx context.Context) {
	log.Println("Modpack update checker started")
	ticker := time.NewTicker(c.tick)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if c.leader != nil && !c.leader.IsLeader() {
					continue
				}
				c.runOnce(ctx)
			}
		}
	}()
}

func (c *ModpackUpdateChecker) runOnce(ctx context.Context) {
	if !c.flags.IsModpacksEnabled(ctx) {
		return // feature disabled -> pause (non-destructive)
	}
	hours := 24
	if s, _ := c.store.GetSetting("modpack_update_check_interval_hours"); s != "" {
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n > 0 {
			hours = n
		}
	}
	staleBefore := time.Now().Add(-time.Duration(hours) * time.Hour)
	due, err := c.store.ListModversionsDueForCheck(staleBefore)
	if err != nil {
		log.Printf("modpack update checker: list due failed: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}

	// Group by (loader, minecraft): the Modrinth endpoint REQUIRES both filters.
	type group struct {
		loader string
		mc     string
		rows   []store.ModversionCheckRow
	}
	groups := map[string]*group{}
	for _, row := range due {
		key := row.Loader + "\x00" + row.Minecraft
		g := groups[key]
		if g == nil {
			g = &group{loader: row.Loader, mc: row.Minecraft}
			groups[key] = g
		}
		g.rows = append(g.rows, row)
	}

	now := time.Now()
	for _, g := range groups {
		for start := 0; start < len(g.rows); start += modpackUpdateChunkSize {
			end := start + modpackUpdateChunkSize
			if end > len(g.rows) {
				end = len(g.rows)
			}
			chunk := g.rows[start:end]
			hashes := make([]string, 0, len(chunk))
			for _, row := range chunk {
				hashes = append(hashes, row.Modversion.SHA1)
			}
			res, err := CheckLatestVersions(hashes, "sha1", []string{g.loader}, []string{g.mc})
			if err != nil {
				log.Printf("modpack update checker: modrinth check failed (loader=%s mc=%s): %v", g.loader, g.mc, err)
				continue // leave this chunk for the next tick
			}
			for _, row := range chunk {
				latest := row.Modversion.ModrinthVersionID // default: current is latest / no match
				if v, ok := res[row.Modversion.SHA1]; ok && v.ID != "" {
					latest = v.ID
				}
				if err := c.store.SetModversionCheckResult(row.Modversion.ID, latest, now); err != nil {
					log.Printf("modpack update checker: persist failed (mv=%d): %v", row.Modversion.ID, err)
				}
			}
		}
	}
}
