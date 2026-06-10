package main

import "embed"

// changelogFS embeds the Markdown-with-frontmatter changelog entries that the
// /api/changelog endpoint surfaces. The `all:` prefix is REQUIRED — without
// it Go's embed package silently skips entries whose path components begin
// with `_` or `.`, and we'd also lose the `dev/` subfolder lookups depending
// on how `embed.FS` resolves them. Keep the prefix; both folders must be
// walkable from services.NewChangelogService.
//
//go:embed all:changelog
var changelogFS embed.FS
