// Package services — Changelog
//
// Embedded, build-time changelog. Parses Markdown-with-frontmatter files from
// two folders inside the Core binary:
//
//   - changelog/*.md      → "Released"
//   - changelog/dev/*.md  → "Coming soon"
//
// The whole file tree is loaded once at boot, parsed, sorted, and cached in
// memory — there is no filesystem I/O at request time. Audience filtering
// (`everyone` vs `admin`) happens at read time so admins and regular users
// share the same cache.
package services

import (
	"embed"
	"io/fs"
	"log"
	"sort"
	"strings"
	"time"
)

// ChangelogEntry is one rendered entry as the panel consumes it.
type ChangelogEntry struct {
	Date     time.Time `json:"date"`     // parsed from frontmatter
	DateStr  string    `json:"dateStr"`  // "2026-06-09" — stable string key + display
	Slug     string    `json:"slug"`     // derived from filename (after the date prefix)
	Type     string    `json:"type"`     // feature|fix|breaking|improvement|security
	Audience string    `json:"audience"` // everyone|admin
	Title    string    `json:"title"`
	Body     string    `json:"body"`    // raw markdown body (no frontmatter)
	Channel  string    `json:"channel"` // "released" | "coming_soon"
}

// ChangelogService is the cached, audience-aware reader.
type ChangelogService struct {
	released   []ChangelogEntry
	comingSoon []ChangelogEntry
}

// NewChangelogService parses both embedded folders. Malformed entries are
// logged and skipped — boot never panics on a broken frontmatter file.
func NewChangelogService(efs embed.FS) (*ChangelogService, error) {
	released, err := loadChangelogFolder(efs, "changelog", "released", false)
	if err != nil {
		return nil, err
	}
	comingSoon, err := loadChangelogFolder(efs, "changelog/dev", "coming_soon", true)
	if err != nil {
		return nil, err
	}
	sortChangelog(released)
	sortChangelog(comingSoon)

	log.Printf("changelog: loaded %d released + %d coming-soon entries", len(released), len(comingSoon))

	return &ChangelogService{
		released:   released,
		comingSoon: comingSoon,
	}, nil
}

// Released returns released entries newest-first, filtered by audience.
func (c *ChangelogService) Released(isAdmin bool) []ChangelogEntry {
	if c == nil {
		return nil
	}
	return filterByAudience(c.released, isAdmin)
}

// ComingSoon returns coming-soon entries newest-first, filtered by audience.
func (c *ChangelogService) ComingSoon(isAdmin bool) []ChangelogEntry {
	if c == nil {
		return nil
	}
	return filterByAudience(c.comingSoon, isAdmin)
}

// CountUnread returns the number of released entries with date > lastSeen,
// audience-filtered. A zero lastSeen means "never seen" — everything counts.
func (c *ChangelogService) CountUnread(lastSeen time.Time, isAdmin bool) int {
	if c == nil {
		return 0
	}
	n := 0
	for _, e := range c.released {
		if e.Audience == "admin" && !isAdmin {
			continue
		}
		if e.Date.After(lastSeen) {
			n++
		}
	}
	return n
}

// LatestReleasedDate returns the newest released entry date, audience-filtered.
// Zero time when no entries are visible.
func (c *ChangelogService) LatestReleasedDate(isAdmin bool) time.Time {
	if c == nil {
		return time.Time{}
	}
	for _, e := range c.released {
		if e.Audience == "admin" && !isAdmin {
			continue
		}
		return e.Date // released is already sorted desc
	}
	return time.Time{}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// loadChangelogFolder walks the immediate children of `dir` inside `efs`
// (NOT recursive — both channels are flat). The `topLevelOnly` flag exists
// for the `changelog` top-level folder, which must not descend into `dev/`.
func loadChangelogFolder(efs embed.FS, dir, channel string, topLevelOnly bool) ([]ChangelogEntry, error) {
	entries, err := efs.ReadDir(dir)
	if err != nil {
		// Folder might not exist in the embedded FS at all — treat as empty.
		// This is gentle for `changelog/dev` if the `all:` prefix were ever
		// dropped from the embed directive.
		return nil, nil
	}
	out := make([]ChangelogEntry, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() {
			// Don't descend — both folders are intentionally flat.
			_ = topLevelOnly
			continue
		}
		name := ent.Name()
		if name == ".gitkeep" || !strings.HasSuffix(strings.ToLower(name), ".md") {
			continue
		}
		path := dir + "/" + name
		raw, err := fs.ReadFile(efs, path)
		if err != nil {
			log.Printf("changelog: %s: read failed: %v", path, err)
			continue
		}
		entry, ok := parseChangelogFile(name, raw)
		if !ok {
			log.Printf("changelog: %s: skipped (malformed or missing required frontmatter)", path)
			continue
		}
		entry.Channel = channel
		out = append(out, entry)
	}
	return out, nil
}

// parseChangelogFile extracts the frontmatter + body from a single markdown
// file. The file must start with a `---` line; the next `---` line closes the
// frontmatter and everything after it is the body.
//
// Filename convention is `YYYY-MM-DD-slug.md`; the date prefix is used to seed
// the slug field, but the canonical date comes from the frontmatter — that way
// a file accidentally renamed without touching the frontmatter still sorts
// correctly.
func parseChangelogFile(filename string, raw []byte) (ChangelogEntry, bool) {
	text := string(raw)
	// Normalize line endings — Windows checkouts may have CRLF.
	text = strings.ReplaceAll(text, "\r\n", "\n")

	if !strings.HasPrefix(text, "---\n") {
		return ChangelogEntry{}, false
	}
	rest := text[len("---\n"):]
	closeIdx := strings.Index(rest, "\n---")
	if closeIdx < 0 {
		return ChangelogEntry{}, false
	}
	fmBlock := rest[:closeIdx]
	body := rest[closeIdx+len("\n---"):]
	body = strings.TrimLeft(body, "\n")

	fm := parseFrontmatter(fmBlock)
	dateStr := fm["date"]
	if dateStr == "" {
		return ChangelogEntry{}, false
	}
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return ChangelogEntry{}, false
	}

	typ := strings.ToLower(strings.TrimSpace(fm["type"]))
	if !validChangelogType(typ) {
		return ChangelogEntry{}, false
	}
	aud := strings.ToLower(strings.TrimSpace(fm["audience"]))
	if aud != "everyone" && aud != "admin" {
		return ChangelogEntry{}, false
	}
	title := strings.TrimSpace(fm["title"])
	if title == "" {
		return ChangelogEntry{}, false
	}

	return ChangelogEntry{
		Date:     t,
		DateStr:  dateStr,
		Slug:     slugFromFilename(filename),
		Type:     typ,
		Audience: aud,
		Title:    title,
		Body:     body,
	}, true
}

// parseFrontmatter does a tiny `key: value` line parse. Whitespace around the
// key and the value is trimmed. Quotes are NOT stripped — values stay literal.
func parseFrontmatter(block string) map[string]string {
	out := make(map[string]string)
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes if present — tolerant of authors who quote.
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[0] == val[len(val)-1] {
			val = val[1 : len(val)-1]
		}
		out[strings.ToLower(key)] = val
	}
	return out
}

func validChangelogType(t string) bool {
	switch t {
	case "feature", "fix", "breaking", "improvement", "security":
		return true
	}
	return false
}

// slugFromFilename strips `.md` and the leading `YYYY-MM-DD-` prefix.
// If the filename doesn't match the convention, returns the filename minus
// the extension so the entry still has a stable key.
func slugFromFilename(name string) string {
	base := strings.TrimSuffix(name, ".md")
	// Look for YYYY-MM-DD- prefix.
	if len(base) > 11 && base[4] == '-' && base[7] == '-' && base[10] == '-' {
		return base[11:]
	}
	return base
}

func sortChangelog(entries []ChangelogEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].Date.Equal(entries[j].Date) {
			return entries[i].Date.After(entries[j].Date)
		}
		// Tiebreak deterministically by slug desc (matches "newer first" intent
		// when two entries land on the same day with lexicographically larger
		// slugs implying later authoring).
		return entries[i].Slug > entries[j].Slug
	})
}

func filterByAudience(in []ChangelogEntry, isAdmin bool) []ChangelogEntry {
	if isAdmin {
		// Admins see everything — return a defensive copy so callers can't
		// mutate the cached slice.
		out := make([]ChangelogEntry, len(in))
		copy(out, in)
		return out
	}
	out := make([]ChangelogEntry, 0, len(in))
	for _, e := range in {
		if e.Audience == "everyone" {
			out = append(out, e)
		}
	}
	return out
}
