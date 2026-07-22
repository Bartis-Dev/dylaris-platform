// Package validate is the single source of truth for user-input validation
// shared by core and node: identifier charsets (username, server / sub-server
// names, slugs, labels), UUIDs, safe relative paths, email, versions and
// resource bounds. One copy prevents the same field being validated
// differently - or not at all - across handlers, and guarantees identifiers are
// safe to interpolate into Redis keys and filesystem paths.
//
// The regexes here were consolidated from these former sites (kept in sync):
// core/handlers registration.go, setup.go, servers.go, servers_lifecycle.go,
// file.go, nodes.go, packs.go. The panel mirrors them in src/lib/validation.ts.
package validate

import (
	"regexp"
	"strings"
)

var (
	// Username is the canonical account-name rule: length 3-32, first char
	// alphanumeric, then alphanumeric / . _ - . It forbids ':' and whitespace,
	// which is what makes a username safe to interpolate into a Redis key such
	// as dylaris:beam:daily:<username>:<date>. (Was registration.go usernameRegex;
	// also replaces setup.go's stricter no-dot variant so all paths agree.)
	Username = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{2,31}$`)

	// ServerName is the unified create+rename rule: length 1-50, first char
	// alphanumeric, then alphanumeric / space / - / + / _ . (Unifies the two
	// former alphabets: servers.go's strip set and servers_lifecycle.go's rename
	// regex disagreed on space vs '_'.)
	ServerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9 \-+_]{0,49}$`)

	// SubServerName is a sub-server directory name: length 1-50, alphanumeric /
	// - / _ / + , no space. It names a directory on the node, so it must never
	// carry path metacharacters.
	SubServerName = regexp.MustCompile(`^[a-zA-Z0-9\-_+]{1,50}$`)

	// ServerUUID is a canonical lowercase-or-upper hex UUID. Enforced because the
	// value is client-supplied at CreateServer and becomes the %s in every
	// dylaris:server:<uuid>:* Redis key and the node's server directory name.
	ServerUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	// Slug is a pack slug: length 2-64, lowercase alphanumeric, inner - / _ .
	// (Was packs.go packSlugRe.)
	Slug = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{1,62}[a-z0-9])?$`)

	// Label is one node label/tag item: length 1-32, first char lower-alnum,
	// then lower alnum / - / _ .
	Label = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

	// Email is a light structural check. (Was registration.go emailRegex.)
	Email = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

	// McVersion is a Minecraft version like 1.21 or 1.21.4.
	McVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)

	// MinecraftUsername is a Mojang account name: 3-16 chars, letters/digits/_.
	MinecraftUsername = regexp.MustCompile(`^[a-zA-Z0-9_]{3,16}$`)

	// serverNameStrip removes any character outside the ServerName alphabet.
	serverNameStrip = regexp.MustCompile(`[^a-zA-Z0-9 \-+_]`)
)

func IsUsername(s string) bool          { return Username.MatchString(s) }
func IsServerName(s string) bool        { return ServerName.MatchString(s) }
func IsSubServerName(s string) bool     { return SubServerName.MatchString(s) }
func IsServerUUID(s string) bool        { return ServerUUID.MatchString(s) }
func IsSlug(s string) bool              { return Slug.MatchString(s) }
func IsLabel(s string) bool             { return Label.MatchString(s) }
func IsEmail(s string) bool             { return Email.MatchString(s) }
func IsMcVersion(s string) bool         { return McVersion.MatchString(s) }
func IsMinecraftUsername(s string) bool { return MinecraftUsername.MatchString(s) }

// SanitizeServerName coerces a raw name into the ServerName alphabet: invalid
// characters are stripped (spaces are kept, they are allowed), the result is
// trimmed to 50 characters and to a valid first character. Returns "" when
// nothing usable remains. This is the create-time best-effort coercion; the
// rename path validates with IsServerName instead.
func SanitizeServerName(raw string) string {
	s := serverNameStrip.ReplaceAllString(raw, "")
	if len(s) > 50 {
		s = s[:50]
	}
	// The first character must be alphanumeric.
	s = strings.TrimLeft(s, " -+_")
	return s
}

// IsSafeRelPath reports whether p is a safe relative path with no traversal: not
// absolute, no Windows drive / UNC prefix, and no ".." segment. Used core-side
// as defence-in-depth in front of the node's own path jail. An empty path means
// the server root and is allowed.
func IsSafeRelPath(p string) bool {
	if p == "" {
		return true
	}
	q := strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(q, "/") {
		return false // absolute
	}
	if len(q) >= 2 && q[1] == ':' {
		return false // windows drive letter
	}
	for _, seg := range strings.Split(q, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// installerTypes is the allowlist of server source/installer types.
var installerTypes = map[string]bool{
	"paper": true, "vanilla": true, "fabric": true, "forge": true, "neoforge": true,
	"library": true, "upload": true, "upload-zip": true, "modpack": true, "pack": true,
}

// IsInstallerType reports whether t is a known server source/installer type.
func IsInstallerType(t string) bool { return installerTypes[t] }

// ResourceBounds validates a server's requested resources. ramMB is megabytes,
// cpu is cores (may be fractional), diskMB is megabytes, hostCPU is the node's
// core count (0 = unknown, so the CPU ceiling is skipped). Returns "" when the
// values are valid, otherwise a human-readable reason.
func ResourceBounds(ramMB int, cpu float64, diskMB int64, hostCPU float64) string {
	if ramMB < 256 {
		return "RAM must be at least 256 MB"
	}
	if cpu <= 0 {
		return "CPU limit must be greater than 0"
	}
	if hostCPU > 0 && cpu > hostCPU {
		return "CPU limit exceeds the node's core count"
	}
	if diskMB < 0 {
		return "disk limit cannot be negative"
	}
	return ""
}
