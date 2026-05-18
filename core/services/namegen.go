package services

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// Curated word lists for container slug names — Heroku-style.
// Kept short and friendly; deliberately avoids ambiguous spellings.

var slugAdjectives = []string{
	"amber", "ancient", "azure", "bold", "brave", "bright", "calm", "clever",
	"cosmic", "crimson", "crisp", "dapper", "deep", "dreamy", "eager", "early",
	"electric", "emerald", "fair", "fancy", "fast", "fiery", "frosty", "gentle",
	"glacial", "golden", "graceful", "grand", "happy", "hardy", "honest", "humble",
	"icy", "indigo", "ivory", "jolly", "keen", "kind", "lively", "lucent",
	"lucky", "mellow", "merry", "midnight", "misty", "noble", "obsidian", "olive",
	"opal", "patient", "pearl", "plucky", "polar", "prairie", "proud", "quartz",
	"quick", "quiet", "radiant", "rapid", "rare", "regal", "rosy", "ruby",
	"rustic", "sable", "scarlet", "shadowy", "silent", "silver", "smooth", "snowy",
	"solar", "sparkly", "spicy", "stellar", "stormy", "sunny", "swift", "thoughtful",
	"tidy", "timely", "tiny", "tranquil", "umber", "valiant", "velvet", "vibrant",
	"violet", "vivid", "warm", "wild", "wise", "witty", "woodland", "young",
	"zealous", "zesty",
}

var slugNouns = []string{
	"badger", "bear", "bison", "bluebird", "cardinal", "cheetah", "cobra", "crane",
	"deer", "dolphin", "dove", "eagle", "ermine", "falcon", "fawn", "ferret",
	"finch", "fox", "gazelle", "gecko", "hare", "hawk", "heron", "hummingbird",
	"hyena", "ibex", "ibis", "iguana", "impala", "jackal", "jaguar", "jay",
	"koala", "lemur", "leopard", "lion", "lynx", "magpie", "marlin", "marmot",
	"meerkat", "merlin", "mink", "mole", "monarch", "mongoose", "mountainlion", "narwhal",
	"newt", "ocelot", "okapi", "orca", "osprey", "otter", "owl", "panda",
	"panther", "pelican", "penguin", "phoenix", "puma", "quail", "rabbit", "raccoon",
	"ram", "raven", "robin", "salamander", "sandpiper", "serval", "sparrow", "stag",
	"starling", "stoat", "swallow", "swan", "tapir", "tern", "thrush", "tiger",
	"toucan", "turtle", "viper", "vole", "vulture", "walrus", "warbler", "weasel",
	"whippet", "wolf", "wolverine", "wombat", "woodpecker", "wren", "yak", "zebra",
	"zorse", "barnowl",
}

const slugSuffixBytes = 2 // 2 bytes → 4 hex chars

// GenerateContainerSlug returns a name like "crimson-otter-7a3f".
// Uses crypto/rand so two simultaneous creations almost never collide.
// The caller is still expected to retry if a name collision is observed.
func GenerateContainerSlug() string {
	adj := pickWord(slugAdjectives)
	noun := pickWord(slugNouns)
	suffix := randomHex(slugSuffixBytes)
	return fmt.Sprintf("%s-%s-%s", adj, noun, suffix)
}

// GenerateUniqueContainerSlug retries up to `maxAttempts` times if the
// caller's `exists` check returns true. After that, returns the last
// attempt regardless — uniqueness should be enforced at the storage layer.
func GenerateUniqueContainerSlug(exists func(name string) bool, maxAttempts int) string {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	var name string
	for i := 0; i < maxAttempts; i++ {
		name = GenerateContainerSlug()
		if !exists(name) {
			return name
		}
	}
	return name
}

func pickWord(words []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		// Fall back to deterministic first entry; collision-resolution
		// still gives the slug uniqueness via the hex suffix.
		return strings.ToLower(words[0])
	}
	return strings.ToLower(words[n.Int64()])
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "0000"
	}
	return hex.EncodeToString(buf)
}
