package embed

import (
	"embed"
	"io/fs"
)

// BeamBinaries embeds the Beam desktop app binaries for all platforms.
// These are built by CI (Wails) and placed here before Core is compiled.
// If the binaries directory is empty, GetBeamBinary returns nil gracefully.
//
//go:embed binaries
var beamFS embed.FS

// GetBeamBinary returns the embedded binary for the given platform.
// Supported platforms: "windows", "macos", "macos-arm", "linux"
// Returns nil if the binary is not found (not yet built).
func GetBeamBinary(platform string) []byte {
	var filename string
	switch platform {
	case "windows":
		filename = "binaries/beam-windows-amd64.exe"
	case "macos":
		filename = "binaries/beam-darwin-amd64"
	case "macos-arm":
		filename = "binaries/beam-darwin-arm64"
	case "linux":
		filename = "binaries/beam-linux-amd64"
	default:
		return nil
	}

	data, err := beamFS.ReadFile(filename)
	if err != nil {
		return nil
	}
	return data
}

// GetBeamFilename returns the appropriate download filename for a platform.
func GetBeamFilename(platform string) string {
	switch platform {
	case "windows":
		return "DylarisBeam.exe"
	case "macos", "macos-arm":
		return "DylarisBeam"
	case "linux":
		return "DylarisBeam"
	default:
		return "DylarisBeam"
	}
}

// DetectPlatform tries to guess the platform from the User-Agent header.
func DetectPlatform(userAgent string) string {
	switch {
	case contains(userAgent, "Windows"):
		return "windows"
	case contains(userAgent, "Macintosh") || contains(userAgent, "Mac OS"):
		return "macos" // Default to Intel, user can override
	case contains(userAgent, "Linux"):
		return "linux"
	default:
		return "windows" // Default fallback
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ListAvailablePlatforms returns which platform binaries are embedded.
func ListAvailablePlatforms() []string {
	var platforms []string
	entries, err := fs.ReadDir(beamFS, "binaries")
	if err != nil {
		return platforms
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch e.Name() {
		case "beam-windows-amd64.exe":
			platforms = append(platforms, "windows")
		case "beam-darwin-amd64":
			platforms = append(platforms, "macos")
		case "beam-darwin-arm64":
			platforms = append(platforms, "macos-arm")
		case "beam-linux-amd64":
			platforms = append(platforms, "linux")
		}
	}
	return platforms
}
