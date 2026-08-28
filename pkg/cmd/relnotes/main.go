// Command relnotes is the CI side of the release-notes format: it validates a
// file, reports the newest version and the services that version names, and
// renders the Discord webhook payload.
//
// It shares the parser with Core (dylaris-pkg/release) on purpose. A second
// implementation in shell would drift, and the first symptom of drift is a
// Discord announcement that disagrees with what the panel shows.
//
//	relnotes validate FILE...
//	relnotes version FILE          newest version, or empty when the file has none
//	relnotes services FILE         services the newest release names, space separated
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
		r, ok := top(one(args))
		if ok {
			fmt.Println(r.Version)
		}
	case "services":
		if r, ok := top(one(args)); ok {
			fmt.Println(strings.Join(r.AllServices(), " "))
		}
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

func one(args []string) string {
	if len(args) != 1 {
		usage()
	}
	return args[0]
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
	role := fs.String("role", "", "Discord role ID to ping; omitted when empty")
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
	if *role != "" {
		payload["content"] = "<@&" + *role + ">"
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
	const more = "- ...\n"
	var b strings.Builder
	for _, e := range entries {
		line := "- " + e.Text
		if len(e.Services) > 0 {
			line += " (`" + strings.Join(e.Services, "`, `") + "`)"
		}
		// Truncate at the ENTRY boundary rather than mid-sentence, so a trimmed
		// field never ends in half a claim about what changed.
		if b.Len()+len(line)+1 > maxFieldValue {
			// ...unless the very first entry is already too long on its own, in
			// which case dropping it would render the category as nothing but an
			// ellipsis. A cut sentence beats a silent one.
			if b.Len() == 0 {
				b.WriteString(truncate(line, maxFieldValue-len(more)-1) + "...")
			}
			b.WriteString(more)
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
