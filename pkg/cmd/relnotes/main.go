// Command relnotes is the CI side of the release-notes format: it validates a
// file, reports the newest version and the services that version names, and
// renders the Discord webhook payload.
//
// It shares the parser with Core (dylaris-pkg/release) on purpose. A second
// implementation in shell would drift, and the first symptom of drift is a
// Discord announcement that disagrees with what the panel shows.
//
//	relnotes validate FILE...
//	relnotes version FILE...       newest version ACROSS the files, or empty
//	relnotes services FILE...      services the newest release names, space separated
//
// version and services take every notes file, not one, because the repo has a
// SINGLE release version and more than one audience. A release that only
// concerns customers adds a block to hosted.md and none to platform.md, and
// reading platform.md alone would then stamp images with a version older than
// the release they were built from - which no component could ever reach.
//
//	relnotes discord -title T [-role ID] [-url U] FILE
//
// Go's flag package stops at the first positional argument, so the flags come
// BEFORE the file. Passing the file first prints usage rather than guessing.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"dylaris-pkg/release"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "validate":
		if len(args) == 0 {
			usage()
		}
		for _, p := range args {
			rs, err := load(p)
			if err != nil {
				fail("%s: %v", p, err)
			}
			fmt.Printf("%s: ok, %d release(s)\n", p, len(rs))
		}
	case "version":
		if v, ok := newest(args); ok {
			fmt.Println(v)
		}
	case "services":
		fmt.Println(strings.Join(servicesInNewest(args), " "))
	case "discord":
		discord(args)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: relnotes validate FILE... | version FILE | services FILE | discord -title T [-role ID] [-url U] FILE")
	os.Exit(2)
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

// newest is the release version of the repo: the newest block across every
// notes file.
func newest(paths []string) (release.Version, bool) {
	if len(paths) == 0 {
		usage()
	}
	var best release.Version
	for _, p := range paths {
		r, ok := top(p)
		if !ok {
			continue
		}
		if best.IsZero() || r.Version.Compare(best) > 0 {
			best = r.Version
		}
	}
	return best, !best.IsZero()
}

// servicesInNewest is every service named by a block that IS the release, in
// any file. Those are the components the release claims changed, and CI checks
// each of them was actually rebuilt - a component named by a release it can
// never reach would read as behind forever with nothing failing.
//
// Older blocks are excluded: they were built when they were written.
func servicesInNewest(paths []string) []string {
	v, ok := newest(paths)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for _, p := range paths {
		r, found := top(p)
		if !found || r.Version.Compare(v) != 0 {
			continue
		}
		for _, s := range r.AllServices() {
			seen[s] = true
		}
	}
	// Ordered by the shared service list so the output is stable.
	var out []string
	for _, s := range release.Services {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out
}

func load(path string) ([]release.Release, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return release.Parse(b)
}

func top(path string) (release.Release, bool) {
	rs, err := load(path)
	if err != nil {
		fail("%s: %v", path, err)
	}
	return release.Latest(rs)
}

// Discord's documented limits. Exceeding either is a 400 from the webhook, so
// the payload is trimmed here rather than discovered in a failing CI step.
//
// maxEmbedTotal is checked against the JSON length, which is always LONGER than
// the embed text it is really a limit on. That is deliberate: the check can
// then only fire early, never late, and being told to shorten a release a
// little before Discord would have refused it is the harmless direction.
const (
	maxFieldValue = 1024
	maxEmbedTotal = 6000

	// maxFieldEntries caps how many entries of a section reach Discord. The
	// message is a NOTICE, not the notes: three lines are what someone reads in
	// a channel, and the embed links to the full file for the rest.
	//
	// It also removes the failure this was written for. Discord's 1024-character
	// field limit was being hit by the third or fourth entry, so the section
	// ended in a bare "- ..." with no indication of how much was missing.
	maxFieldEntries = 3

	// moreReserve is space kept free for the trailing "and N more" line, so
	// appending it can never be what pushes the field over the limit.
	moreReserve = 48
)

// Colours by the most serious thing in the release, so the channel is skimmable
// without reading: a mandatory update or a breaking change is red, a security
// fix orange, anything else the brand violet.
const (
	colourAction   = 0xE5484D
	colourSecurity = 0xF5A524
	colourNormal   = 0x8B5CF6
)

func discord(args []string) {
	fs := flag.NewFlagSet("discord", flag.ExitOnError)
	title := fs.String("title", "", "audience name shown before the version, e.g. \"Platform\"")
	role := fs.String("role", "", "comma-separated Discord role IDs to ping; empty entries are dropped")
	url := fs.String("url", "", "link to the full notes")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 || *title == "" {
		usage()
	}
	r, ok := top(fs.Arg(0))
	if !ok {
		fail("%s: no releases to announce", fs.Arg(0))
	}

	var desc []string
	if r.Required != nil {
		when := "immediately"
		if !r.Required.Immediate {
			when = "by " + r.Required.Deadline.Format("2006-01-02 15:04 UTC")
		}
		line := fmt.Sprintf("**This update is mandatory %s.**", when)
		if r.Required.Note != "" {
			line += " " + r.Required.Note
		}
		desc = append(desc, line)
	}
	if svc := r.AllServices(); len(svc) > 0 {
		desc = append(desc, "Services to update: `"+strings.Join(svc, "`, `")+"`")
	} else {
		desc = append(desc, "Nothing to update on your side.")
	}
	if *url != "" {
		desc = append(desc, "["+"Full notes"+"]("+*url+")")
	}

	// All four categories, always, even when empty: a reader must be able to
	// tell "no security fixes this time" from "nobody filled this in".
	type field struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	}
	var fields []field
	for _, name := range []string{"Features", "Breaking", "Security", "Fixes"} {
		fields = append(fields, field{Name: name, Value: renderEntries(r.Entries(name))})
	}

	colour := colourNormal
	switch {
	case r.Required != nil || len(r.Breaking) > 0:
		colour = colourAction
	case len(r.Security) > 0:
		colour = colourSecurity
	}

	embed := map[string]any{
		"title":       fmt.Sprintf("%s %s", *title, r.Version),
		"description": strings.Join(desc, "\n"),
		"color":       colour,
	}
	if *url != "" {
		embed["url"] = *url
	}
	embed["fields"] = fields

	payload := map[string]any{"embeds": []any{embed}}
	// The ping has to sit in content: a role mention inside an embed renders as
	// a mention but notifies nobody.
	//
	// Empty entries are dropped rather than rendered. One audience is pinged
	// through two roles, and a role whose secret is unset would otherwise post a
	// literal "<@&>" - visible nonsense in a channel people are told to trust.
	if ping := mentions(*role); ping != "" {
		payload["content"] = ping
	}

	out, err := json.Marshal(payload)
	if err != nil {
		fail("marshal: %v", err)
	}
	if len(out) > maxEmbedTotal {
		fail("payload is %d bytes, over Discord's %d limit; split the release or shorten the entries",
			len(out), maxEmbedTotal)
	}
	fmt.Println(string(out))
}

// mentions renders a comma-separated role list as Discord mentions, skipping
// blanks and duplicates so an unset role secret cannot post an empty mention.
func mentions(list string) string {
	var out []string
	seen := map[string]bool{}
	for _, id := range strings.Split(list, ",") {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, "<@&"+id+">")
	}
	return strings.Join(out, " ")
}

// truncate cuts to at most n BYTES without splitting a rune, so a trimmed line
// cannot end in half a character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func renderEntries(entries []release.Entry) string {
	if len(entries) == 0 {
		return "Nothing this time."
	}
	var b strings.Builder
	shown := 0
	for _, e := range entries {
		if shown == maxFieldEntries {
			break
		}
		line := "- " + e.Text
		if len(e.Services) > 0 {
			line += " (`" + strings.Join(e.Services, "`, `") + "`)"
		}
		// Cut at the ENTRY boundary rather than mid-sentence, so a trimmed field
		// never ends in half a claim about what changed.
		if b.Len()+len(line)+1 > maxFieldValue-moreReserve {
			// ...unless the very first entry is already too long on its own, in
			// which case dropping it would render the category as nothing but a
			// pointer. A cut sentence beats a silent one.
			if shown == 0 {
				b.WriteString(truncate(line, maxFieldValue-moreReserve-4) + "...\n")
				shown = 1
			}
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
		shown++
	}
	if rest := len(entries) - shown; rest > 0 {
		fmt.Fprintf(&b, "- ...and %d more, in the full notes\n", rest)
	}
	return strings.TrimRight(b.String(), "\n")
}
