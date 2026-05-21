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

// buildStartCommand assembles the full `java …` invocation for an
// installed sub-server. The platform -Xms/-Xmx is always the LAST JVM
// argument before the main-class token (-jar / @argsfile), so it wins
// over anything the user put in extraJvmFlags or user_jvm_args.txt.
func buildStartCommand(subServerDir string, memMB int, extraJvmFlags string) (string, error) {
	lf := resolveLaunch(subServerDir)
	mem := fmt.Sprintf("-Xms%dM -Xmx%dM", memMB, memMB)
	parts := []string{"java"}
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	switch lf.Mode {
	case launchJar:
		add(extraJvmFlags)
		add(mem)
		parts = append(parts, "-jar", lf.Jar, "nogui")
	case launchArgfile:
		add(extraJvmFlags)
		if lf.UserJvmArgs {
			add("@user_jvm_args.txt")
		}
		add(mem)
		parts = append(parts, "@"+lf.ArgsFile, "nogui")
	default:
		return "", fmt.Errorf("no runnable server found in %s", subServerDir)
	}
	return strings.Join(parts, " "), nil
}
