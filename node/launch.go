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

// buildStartCommand assembles the full `java …` invocation for an
// installed sub-server. The platform -Xms/-Xmx is always the LAST JVM
// argument before the main-class token (-jar / @argsfile), so it wins
// over anything the user put in extraJvmFlags or user_jvm_args.txt.
func buildStartCommand(subServerDir string, memMB int, extraJvmFlags string) (string, error) {
	lf := resolveLaunch(subServerDir)
	parts := []string{"java"}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
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
