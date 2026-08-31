package main

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Answering "can this machine run a node" before anyone tries.
//
// Setting a node up is a host operation, and every failure it has looks the same
// from the panel: the machine is configured, the node never appears, and the
// reason is on the host in a log nobody opened. Docker missing, the daemon not
// running, the user not in the docker group - three different problems that all
// present as silence.
//
// This runs the checks the app can actually run, and says which one failed in
// the words that name the fix. It is deliberately the whole of the first half of
// the feature: the diagnosis is what people cannot do for themselves, while
// running a compose file is something they can, given the file and the reason it
// would not have worked.

// DeployCheck is one probe's result. Ok false with Fix set is the useful case;
// Fix is what the user does about it, in a sentence, not a category.
type DeployCheck struct {
	Name string `json:"name"`
	Ok   bool   `json:"ok"`
	// Detail is what was observed - a version string, or the error's own words.
	Detail string `json:"detail,omitempty"`
	// Fix is empty when Ok. Never a bare category ("permission error"): it names
	// the action, because the person reading it is not the person who wrote the
	// code.
	Fix string `json:"fix,omitempty"`
}

// DeployEnvironment is the whole report. Ready is the single answer the UI gates
// its button on, so no screen has to re-derive it from the list.
type DeployEnvironment struct {
	Ready  bool          `json:"ready"`
	OS     string        `json:"os"`
	Checks []DeployCheck `json:"checks"`
}

// probeTimeout bounds each command. A docker CLI talking to a daemon that is
// starting up, or to a broken remote context, can hang for a long time - and a
// diagnostic screen that hangs is worse than one that says "it did not answer".
const probeTimeout = 12 * time.Second

// runProbe executes one command and returns its combined output.
//
// Combined on purpose: the docker CLI writes the interesting part of most
// failures to stderr, and a probe that reported only stdout would show an empty
// string for exactly the cases this exists to explain.
func runProbe(ctx context.Context, name string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(c, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// CheckDeployEnvironment reports whether a node could be deployed here.
//
// Bound to the frontend and READ-ONLY, so it needs no shell token: it starts
// nothing, writes nothing, and tells the caller only what any user of this
// machine could find out by typing the same two commands.
func (a *App) CheckDeployEnvironment() *DeployEnvironment {
	ctx := context.Background()
	env := &DeployEnvironment{OS: runtime.GOOS}

	// 1. Is there a docker CLI at all. Everything else is meaningless without it,
	// so a failure here short-circuits: reporting three more errors that all say
	// "docker not found" buries the one line that matters.
	path, err := exec.LookPath("docker")
	if err != nil {
		env.Checks = append(env.Checks, DeployCheck{
			Name:   "Docker installed",
			Detail: "no docker command on this machine",
			Fix:    dockerInstallHint(),
		})
		return env
	}
	env.Checks = append(env.Checks, DeployCheck{Name: "Docker installed", Ok: true, Detail: path})

	// 2. Is the daemon reachable, and are we allowed to talk to it. One probe
	// answers both, and the two are told apart by what the error says - which is
	// the only thing that distinguishes them from out here.
	if out, err := runProbe(ctx, "docker", "info", "--format", "{{.ServerVersion}}"); err != nil {
		env.Checks = append(env.Checks, DeployCheck{
			Name:   "Docker running",
			Detail: firstLine(out),
			Fix:    daemonFix(out),
		})
		return env
	} else {
		env.Checks = append(env.Checks, DeployCheck{
			Name: "Docker running", Ok: true, Detail: "engine " + firstLine(out),
		})
	}

	// 3. Compose v2, which is what the generated file needs. Present as a docker
	// subcommand on every current install; the standalone docker-compose binary
	// is v1 and will not read this file, so it is not accepted as a substitute.
	if out, err := runProbe(ctx, "docker", "compose", "version", "--short"); err != nil {
		env.Checks = append(env.Checks, DeployCheck{
			Name:   "Docker Compose",
			Detail: firstLine(out),
			Fix: "This Docker has no 'docker compose' subcommand. It ships with Docker Desktop and " +
				"with the docker-compose-plugin package on Linux. The older standalone docker-compose " +
				"command is version 1 and cannot read this file.",
		})
		return env
	} else {
		env.Checks = append(env.Checks, DeployCheck{
			Name: "Docker Compose", Ok: true, Detail: "v" + firstLine(out),
		})
	}

	env.Ready = true
	return env
}

// daemonFix turns the docker CLI's own failure into an instruction.
//
// Matched on the message rather than the exit code, because the CLI returns 1
// for all of these. The words are stable across versions and platforms; an
// unrecognised failure falls through to the honest generic answer rather than
// guessing, since a confidently wrong instruction costs more than none.
func daemonFix(out string) string {
	l := strings.ToLower(out)
	switch {
	case strings.Contains(l, "permission denied"):
		if runtime.GOOS == "linux" {
			return "Docker is running but this account may not talk to it. Add yourself to the " +
				"docker group (sudo usermod -aG docker $USER), then log out and back in - a new " +
				"terminal is not enough, the group is attached at login."
		}
		return "Docker is running but this account may not talk to it. Check that your user has " +
			"access to Docker on this machine."
	case strings.Contains(l, "cannot connect") || strings.Contains(l, "is the docker daemon running") ||
		strings.Contains(l, "docker_engine") || strings.Contains(l, "dockerdesktoplinuxengine"):
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			return "Docker is installed but not running. Start Docker Desktop and wait until it " +
				"reports that the engine is running, then check again."
		}
		return "Docker is installed but the daemon is not running. Start it with " +
			"'sudo systemctl start docker', then check again."
	case strings.Contains(l, "context") && strings.Contains(l, "not found"):
		return "The active Docker context points at something that is not there. " +
			"Run 'docker context use default' and check again."
	default:
		return "Docker did not answer. The message above is what it said."
	}
}

// dockerInstallHint names where to get Docker, per platform. No download is
// performed: installing an engine needs administrator rights and a reboot on
// Windows, and a half-finished install started by another program is a support
// case nobody can diagnose remotely.
func dockerInstallHint() string {
	switch runtime.GOOS {
	case "windows":
		return "Install Docker Desktop from docker.com/products/docker-desktop, then start it and check again."
	case "darwin":
		return "Install Docker Desktop from docker.com/products/docker-desktop, or 'brew install --cask docker', then start it and check again."
	default:
		return "Install Docker Engine and the compose plugin - on Debian/Ubuntu: " +
			"'curl -fsSL https://get.docker.com | sh'. Then check again."
	}
}

// firstLine keeps a report readable when a command answers with a stack of them.
// The first line of a docker error is the one that names the problem; the rest
// is usually a repeat of the command that failed.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
