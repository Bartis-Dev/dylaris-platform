package main

import (
	"os"
	"path/filepath"
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
