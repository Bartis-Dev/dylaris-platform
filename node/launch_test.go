package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLaunch(t *testing.T) {
	t.Run("paper jar", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "paper-1.20.1-196.jar"), []byte("x"), 0644)
		lf := resolveLaunch(dir)
		if lf.Mode != launchJar || lf.Jar != "paper-1.20.1-196.jar" {
			t.Fatalf("got %+v", lf)
		}
	})
	t.Run("modern forge argfile", func(t *testing.T) {
		dir := t.TempDir()
		args := filepath.Join(dir, "libraries/net/minecraftforge/forge/1.20.1-47.2.0")
		os.MkdirAll(args, 0755)
		os.WriteFile(filepath.Join(args, "unix_args.txt"), []byte("x"), 0644)
		os.WriteFile(filepath.Join(dir, "user_jvm_args.txt"), []byte("x"), 0644)
		lf := resolveLaunch(dir)
		if lf.Mode != launchArgfile {
			t.Fatalf("expected argfile, got %+v", lf)
		}
		if lf.ArgsFile != "libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt" {
			t.Fatalf("argsfile: %s", lf.ArgsFile)
		}
		if !lf.UserJvmArgs {
			t.Fatalf("expected user_jvm_args.txt detected")
		}
	})
	t.Run("neoforge argfile", func(t *testing.T) {
		dir := t.TempDir()
		args := filepath.Join(dir, "libraries/net/neoforged/neoforge/20.4.80")
		os.MkdirAll(args, 0755)
		os.WriteFile(filepath.Join(args, "unix_args.txt"), []byte("x"), 0644)
		lf := resolveLaunch(dir)
		if lf.Mode != launchArgfile {
			t.Fatalf("expected argfile, got %+v", lf)
		}
	})
	t.Run("nothing runnable", func(t *testing.T) {
		lf := resolveLaunch(t.TempDir())
		if lf.Mode != launchNone {
			t.Fatalf("expected none, got %+v", lf)
		}
	})
}
