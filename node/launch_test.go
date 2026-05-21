package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLaunch(t *testing.T) {
	t.Run("paper jar", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "paper-1.20.1-196.jar"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		lf := resolveLaunch(dir)
		if lf.Mode != launchJar || lf.Jar != "paper-1.20.1-196.jar" {
			t.Fatalf("got %+v", lf)
		}
	})
	t.Run("modern forge argfile", func(t *testing.T) {
		dir := t.TempDir()
		args := filepath.Join(dir, "libraries/net/minecraftforge/forge/1.20.1-47.2.0")
		if err := os.MkdirAll(args, 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(args, "unix_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "user_jvm_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
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
		if err := os.MkdirAll(args, 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(args, "unix_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		lf := resolveLaunch(dir)
		if lf.Mode != launchArgfile {
			t.Fatalf("expected argfile, got %+v", lf)
		}
		if lf.ArgsFile != "libraries/net/neoforged/neoforge/20.4.80/unix_args.txt" {
			t.Fatalf("argsfile: %s", lf.ArgsFile)
		}
	})
	t.Run("plain server.jar fallback", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "server.jar"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		lf := resolveLaunch(dir)
		if lf.Mode != launchJar {
			t.Fatalf("expected launchJar, got %+v", lf)
		}
		if lf.Jar != "server.jar" {
			t.Fatalf("jar: %s", lf.Jar)
		}
	})
	t.Run("nothing runnable", func(t *testing.T) {
		lf := resolveLaunch(t.TempDir())
		if lf.Mode != launchNone {
			t.Fatalf("expected none, got %+v", lf)
		}
	})
}

func TestBuildStartCommand(t *testing.T) {
	t.Run("jar form puts platform Xmx last before -jar", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "paper-1.20.1.jar"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		cmd, err := buildStartCommand(dir, 4096, "-XX:+UseG1GC")
		if err != nil {
			t.Fatal(err)
		}
		want := "java -XX:+UseG1GC -Xms4096M -Xmx4096M -jar paper-1.20.1.jar nogui"
		if cmd != want {
			t.Fatalf("got %q want %q", cmd, want)
		}
	})
	t.Run("argfile form puts platform Xmx after user_jvm_args, before args file", func(t *testing.T) {
		dir := t.TempDir()
		args := filepath.Join(dir, "libraries/net/minecraftforge/forge/1.20.1-47.2.0")
		if err := os.MkdirAll(args, 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(args, "unix_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "user_jvm_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		cmd, err := buildStartCommand(dir, 6144, "")
		if err != nil {
			t.Fatal(err)
		}
		want := "java @user_jvm_args.txt -Xms6144M -Xmx6144M " +
			"@libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt nogui"
		if cmd != want {
			t.Fatalf("got %q want %q", cmd, want)
		}
	})
	t.Run("argfile form: extra flags precede user_jvm_args", func(t *testing.T) {
		dir := t.TempDir()
		args := filepath.Join(dir, "libraries/net/minecraftforge/forge/1.20.1-47.2.0")
		if err := os.MkdirAll(args, 0755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(args, "unix_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "user_jvm_args.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		cmd, err := buildStartCommand(dir, 4096, "-Dfoo=bar")
		if err != nil {
			t.Fatal(err)
		}
		want := "java -Dfoo=bar @user_jvm_args.txt -Xms4096M -Xmx4096M " +
			"@libraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt nogui"
		if cmd != want {
			t.Fatalf("got %q want %q", cmd, want)
		}
	})
	t.Run("nothing runnable is an error", func(t *testing.T) {
		if _, err := buildStartCommand(t.TempDir(), 1024, ""); err == nil {
			t.Fatal("expected error for empty dir")
		}
	})
}
