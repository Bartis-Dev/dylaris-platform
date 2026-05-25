package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type launchMode int

const (
	launchNone launchMode = iota
	launchJar
	launchArgfile
)

// launchForm describes how a sub-server directory must be started.
type launchForm struct {
	Mode        launchMode
	Jar         string // launchJar: jar filename relative to the sub-server dir
	ArgsFile    string // launchArgfile: unix_args.txt path relative to the dir
	UserJvmArgs bool   // launchArgfile: user_jvm_args.txt exists
}

// resolveLaunch inspects an installed sub-server directory and reports how
// it must be launched. Modern Forge / NeoForge ship an args file under
// libraries/... and no runnable jar; everything else ships a jar.
func resolveLaunch(subServerDir string) launchForm {
	for _, glob := range []string{
		"libraries/net/minecraftforge/forge/*/unix_args.txt",
		"libraries/net/neoforged/neoforge/*/unix_args.txt",
	} {
		matches, _ := filepath.Glob(filepath.Join(subServerDir, glob))
		if len(matches) > 0 {
			rel, _ := filepath.Rel(subServerDir, matches[0])
			_, ujErr := os.Stat(filepath.Join(subServerDir, "user_jvm_args.txt"))
			return launchForm{
				Mode:        launchArgfile,
				ArgsFile:    filepath.ToSlash(rel),
				UserJvmArgs: ujErr == nil,
			}
		}
	}
	if jar := DetectServerJar(subServerDir); jar != "" && jar != "server.jar" {
		return launchForm{Mode: launchJar, Jar: jar}
	}
	// DetectServerJar returns "server.jar" as a fallback; only trust it if
	// the file actually exists.
	if _, err := os.Stat(filepath.Join(subServerDir, "server.jar")); err == nil {
		return launchForm{Mode: launchJar, Jar: "server.jar"}
	}
	return launchForm{Mode: launchNone}
}

// extractJvmFlagsFromCommand pulls out the user/admin JVM flags from an
// existing start-command string so they can be forwarded to buildStartCommand.
// It strips the structural tokens (java, -Xms/-Xmx, -jar <jar>, @<argsfile>,
// nogui) and returns whatever remains — typically Aikar flags or custom flags
// set by the admin. Works with both the legacy Core format
// ("java -Xms -Xmx <flags> -jar …") and the buildStartCommand format
// ("java <flags> -Xms -Xmx -jar …" / "java <flags> @argsfile …").
// Returns "" when the command is empty or contains no recognised extra flags.
func extractJvmFlagsFromCommand(cmd string) string {
	parts := strings.Fields(cmd)
	var flags []string
	skipNext := false
	for _, p := range parts {
		if skipNext {
			skipNext = false
			continue
		}
		switch {
		case p == "java":
		case strings.HasPrefix(p, "-Xms"), strings.HasPrefix(p, "-Xmx"):
		case p == "-jar":
			skipNext = true // skip the jar filename that follows
		case p == "nogui":
		case strings.HasPrefix(p, "@"): // @unix_args.txt / @user_jvm_args.txt
		default:
			flags = append(flags, p)
		}
	}
	return strings.Join(flags, " ")
}

// subServerType classifies an installed sub-server directory into a
// human-readable type string by inspecting its launch form.
func subServerType(subServerDir string) string {
	lf := resolveLaunch(subServerDir)
	switch lf.Mode {
	case launchArgfile:
		if strings.Contains(lf.ArgsFile, "minecraftforge") {
			return "forge"
		}
		if strings.Contains(lf.ArgsFile, "neoforged") {
			return "neoforge"
		}
		return "unknown"
	case launchJar:
		jar := lf.Jar
		if strings.HasPrefix(jar, "fabric-server") || strings.Contains(jar, "fabric") {
			return "fabric"
		}
		if strings.HasPrefix(jar, "paper-") {
			return "paper"
		}
		if strings.HasPrefix(jar, "purpur-") {
			return "purpur"
		}
		return "vanilla"
	default:
		return "unknown"
	}
}

// gcLogFlag is the unified-logging directive that makes the JVM print
// G1/parallel GC summary lines like "GC(0) Pause Young ... 256M->50M(2048M)"
// to stdout. The log-shipper parses these lines to surface live JVM heap
// usage to the panel -- without this the only memory metric we can show
// is the container's anon-RSS, which sits at Xmx forever because we force
// Xms=Xmx and Java never gives heap back to the OS.
//
// Java 11+ accepts -Xlog. Java 8 will fail to start if it sees this flag,
// so we omit it for Java-8 images (detected by the configured Java image
// tag in buildStartCommand). Java 8 servers fall back to the old
// container-level memory metric on the panel.
const gcLogFlag = "-Xlog:gc::utctime,level,tags"

// buildStartCommand assembles the full `java …` invocation for an
// installed sub-server. The platform -Xms/-Xmx is always the LAST JVM
// argument before the main-class token (-jar / @argsfile), so it wins
// over anything the user put in extraJvmFlags or user_jvm_args.txt.
//
// javaImage is used to detect Java 8 (which rejects -Xlog) so we can
// skip the GC-logging flag in that case. Pass an empty string to keep
// the flag on by default (Java 11+ behavior).
func buildStartCommand(subServerDir string, memMB int, extraJvmFlags string, javaImage string) (string, error) {
	lf := resolveLaunch(subServerDir)
	parts := []string{"java"}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	// Inject GC logging first so it survives any user-extraFlags reorder.
	if !strings.Contains(javaImage, "java8") {
		add(gcLogFlag)
	}
	switch lf.Mode {
	case launchJar:
		add(extraJvmFlags)
		parts = append(parts, fmt.Sprintf("-Xms%dM", memMB), fmt.Sprintf("-Xmx%dM", memMB))
		parts = append(parts, "-jar", lf.Jar, "nogui")
	case launchArgfile:
		add(extraJvmFlags)
		if lf.UserJvmArgs {
			add("@user_jvm_args.txt")
		}
		parts = append(parts, fmt.Sprintf("-Xms%dM", memMB), fmt.Sprintf("-Xmx%dM", memMB))
		parts = append(parts, "@"+lf.ArgsFile, "nogui")
	default:
		return "", fmt.Errorf("no runnable server found in %s", subServerDir)
	}
	return strings.Join(parts, " "), nil
}
