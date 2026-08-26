package main

// Move a server to another Minecraft version and carry its mods along, as ONE
// command.
//
// It is one command on purpose. The queue consumer runs with Concurrency = 8,
// so separate "swap the mods" and "reinstall" commands have no ordering between
// them: a reinstall ends by starting the container, and a mod download still in
// flight would land in a server that is already running. Sequencing it here is
// the only way the whole move lands together.
//
// The reinstall step keeps the world and the mods directory - CleanServerJars
// only removes root jars plus versions/cache/.fabric - which is what makes
// carrying mods across a version change possible at all.
//
// update_server_version payload (from core):
//
//	uuid, activeSubServer, targetDir, remove[], install[]{fileName,downloadUrl,sha512}
//	plus the usual docker + installer blocks
//
// Every field that becomes part of a path is re-checked here, same as
// install_mod: this arrives over a queue, and the node is the last thing
// between it and the filesystem.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"dylaris-pkg/validate"

	"github.com/redis/go-redis/v9"
)

type versionUpdateFile struct {
	FileName    string `json:"fileName"`
	DownloadURL string `json:"downloadUrl"`
	SHA512      string `json:"sha512"`
}

type versionUpdatePayload struct {
	UUID            string              `json:"uuid"`
	ActiveSubServer string              `json:"activeSubServer"`
	TargetDir       string              `json:"targetDir"`
	Remove          []string            `json:"remove"`
	Install         []versionUpdateFile `json:"install"`
}

// runUpdateServerVersion performs the whole move: stop, drop the named jars,
// fetch the new ones, reinstall the server software at the new version, restart.
func runUpdateServerVersion(ctx context.Context, rdb *redis.Client, dm *DockerManager, storage *StorageManager, cmd NodeCommand, payload string) {
	var pl versionUpdatePayload
	if err := json.Unmarshal([]byte(payload), &struct {
		Config *versionUpdatePayload `json:"config"`
	}{Config: &pl}); err != nil {
		log.Printf("update_server_version: decode failed: %v", err)
		return
	}
	if pl.UUID == "" {
		log.Printf("update_server_version: no server uuid in payload")
		return
	}
	subName := pl.ActiveSubServer
	if subName == "" {
		subName = "server"
	}
	if !validActiveSubServer(subName) {
		log.Printf("update_server_version: invalid active sub-server %q", subName)
		return
	}
	targetDir := pl.TargetDir
	if targetDir == "" {
		targetDir = "mods"
	}
	if !validSubDir(targetDir) {
		log.Printf("update_server_version: invalid target dir %q", targetDir)
		return
	}
	serverPath := storage.GetServerDir(pl.UUID)
	if serverPath == "" {
		log.Printf("update_server_version: storage lookup for %s returned empty path", pl.UUID)
		return
	}

	if !commandsInFlight.enter("version_update:" + pl.UUID) {
		log.Printf("update_server_version: an update of %s is already running on this node, ignoring the duplicate", pl.UUID)
		return
	}
	defer commandsInFlight.leave("version_update:" + pl.UUID)

	statusKey := fmt.Sprintf("dylaris:server:%s:status", pl.UUID)
	// Hold the reconciler off for the whole move, exactly like reinstall: the
	// container is about to be stopped while its desired_state is still online.
	releaseBusy := holdBusyStatus(rdb, pl.UUID, "installing", busyStatusTTL)
	defer releaseBusy()
	rdb.Del(ctx, fmt.Sprintf("dylaris:server:%s:reconcile_failed", pl.UUID))
	rdb.Set(ctx, fmt.Sprintf("dylaris:server:%s:install-start", pl.UUID), "1", 30*time.Second)

	log.Printf("update_server_version: moving %s/%s (%d removals, %d installs)", pl.UUID, subName, len(pl.Remove), len(pl.Install))

	dm.PowerAction(pl.UUID, "stop")
	time.Sleep(3 * time.Second)

	// Same traversal + symlink boundary install_mod goes through: the mods
	// directory is bind-mounted into the tenant's own container, so a symlink
	// planted there would otherwise redirect every write and delete below.
	modsDir, err := resolveWithinDir(serverPath, filepath.Join(subName, targetDir))
	if err != nil {
		log.Printf("update_server_version: %v", err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}
	if err := os.MkdirAll(modsDir, 0o755); err != nil {
		log.Printf("update_server_version: mkdir %s: %v", modsDir, err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}

	for _, name := range pl.Remove {
		if !validate.IsPlainFileName(name) {
			log.Printf("update_server_version: skipping invalid removal name %q", name)
			continue
		}
		p := filepath.Join(modsDir, name)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Printf("update_server_version: rm %s: %v", p, err)
		}
	}

	// Downloads happen before the reinstall so nothing is still arriving when
	// the reinstall starts the container at the end.
	installed := 0
	for _, f := range pl.Install {
		if !validate.IsPlainFileName(f.FileName) {
			log.Printf("update_server_version: skipping invalid install name %q", f.FileName)
			continue
		}
		if err := validateModrinthURL(f.DownloadURL); err != nil {
			log.Printf("update_server_version: %v", err)
			continue
		}
		tmpFile, err := resolveWithinDir(modsDir, f.FileName+".part")
		if err != nil {
			log.Printf("update_server_version: %v", err)
			continue
		}
		if err := downloadAndVerify(f.DownloadURL, tmpFile, f.SHA512); err != nil {
			os.Remove(tmpFile)
			log.Printf("update_server_version: download failed for %s: %v", f.FileName, err)
			continue
		}
		if err := os.Rename(tmpFile, filepath.Join(modsDir, f.FileName)); err != nil {
			os.Remove(tmpFile)
			log.Printf("update_server_version: rename %s: %v", f.FileName, err)
			continue
		}
		installed++
	}
	log.Printf("update_server_version: %s/%s now has %d of %d new jars in place", pl.UUID, subName, installed, len(pl.Install))

	subServerDir := filepath.Join(serverPath, subName)
	if err := CleanServerJars(subServerDir); err != nil {
		log.Printf("update_server_version: clean failed for %s/%s: %v", pl.UUID, subName, err)
	}

	installerCfg := cmd.Installer
	installerCfg.JavaImage = cmd.Config.Docker.Image
	installerCfg.ServerUUID = pl.UUID
	if err := InstallServer(serverPath, subName, installerCfg); err != nil {
		log.Printf("update_server_version: install failed for %s/%s: %v", pl.UUID, subName, err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}

	startCmd, err := buildStartCommand(subServerDir, cmd.Config.Docker.RAM, cmd.Config.Docker.ExtraJvmFlags, cmd.Config.Docker.Image)
	if err != nil {
		log.Printf("update_server_version: buildStartCommand failed for %s/%s: %v", pl.UUID, subName, err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}
	cmd.Config.Docker.Command = startCmd
	if err := dm.RecreateWithCommand(cmd.Config); err != nil {
		log.Printf("update_server_version: failed to restart %s: %v", pl.UUID, err)
		rdb.Set(ctx, statusKey, "stopped", 30*time.Second)
		return
	}
	log.Printf("update_server_version: %s/%s moved and running", pl.UUID, subName)
}
