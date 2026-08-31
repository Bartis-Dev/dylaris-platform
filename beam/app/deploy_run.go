package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Writing the compose file and starting it.
//
// The second half of "deploy on this machine". The first half (deploy_check.go)
// is read-only and needs no permission; this one writes to disk and starts a
// container, so it is shell-token gated like every other side-effecting binding.
// That gate is the reason this lives in the app shell rather than being called
// from the proxied panel: the token is deliberately absent there, so a panel that
// had been tampered with cannot start containers on the machines of everyone
// viewing it.

// DeployRequest is what the shell UI collects. Nothing here is a secret except
// EnrollToken, which is single-use and expires - it is written into the compose
// file because that is where the node reads it from, and it is consumed on the
// node's first successful pairing.
type DeployRequest struct {
	// Dir is where the compose file goes. Empty means the app's own data
	// directory, which is the one place it knows it may write.
	Dir string `json:"dir"`
	// NodeID is the stable identity for this machine.
	NodeID string `json:"nodeId"`
	// EnrollToken is the single-use pairing token minted by Core.
	EnrollToken string `json:"enrollToken"`
	// CoreGRPCAddr is host:port for the node's control channel.
	CoreGRPCAddr string `json:"coreGrpcAddr"`
	// TLSFingerprint pins Core's gRPC certificate. Empty when Core runs the
	// channel without TLS, in which case the node must not be told to pin one.
	TLSFingerprint string `json:"tlsFingerprint"`
	// Start runs the stack after writing. False writes the file and stops, which
	// is what someone reviewing it before running it wants.
	Start bool `json:"start"`
}

// DeployResult reports what happened, in the order it happened. Log is the
// command output verbatim: this is the one screen where the docker CLI's own
// words are more useful than anything this app could say about them.
type DeployResult struct {
	Ok          bool   `json:"ok"`
	ComposePath string `json:"composePath"`
	Started     bool   `json:"started"`
	Log         string `json:"log,omitempty"`
	Error       string `json:"error,omitempty"`
}

// nodeIDPattern is what a node identity may contain. It ends up in a container
// name and in Redis key paths, so it is restricted to what is safe in both
// rather than to what Docker happens to tolerate.
var nodeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,62}$`)

// hostPortPattern is host:port with no scheme and no path. The node dials this
// directly, and a value carrying a scheme fails at connect time with an error
// that does not mention the setting.
var hostPortPattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+:[0-9]{1,5}$`)

// composeTemplate is the file written for an external node.
//
// host networking is deliberate and is the same choice the documented external
// deployment makes: the node publishes game servers on ports of the host it runs
// on, and a bridge network would put them behind a NAT the players cannot reach.
//
// No CLUSTER_SECRET. The node fetches a scoped Redis credential over gRPC once
// it has paired; handing a customer machine the fleet credential would undo the
// entire point of per-node scoping.
const composeTemplate = `# Written by Dylaris Beam. Safe to keep, edit and re-run.
#
# The enroll token below is SINGLE USE and expires. Once this node has paired it
# is spent - keeping the file is fine, but re-running it on a second machine will
# not pair a second node.
services:
  node:
    image: %s
    restart: unless-stopped
    network_mode: host
    environment:
      NODE_ID: "%s"
      NODE_ENROLL_TOKEN: "%s"
      CORE_GRPC_ADDR: "%s"
%s    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - node_data:/app/dylaris_data
    cap_add: [SYS_ADMIN]

volumes:
  node_data:
`

// defaultNodeImage is the published node image. A constant rather than something
// the caller passes: the compose file is generated for a specific Core, and
// letting a UI field choose the image would make "deploy on this machine" a way
// to run an arbitrary container with the docker socket mounted.
const defaultNodeImage = "ghcr.io/bartis-dev/dylaris-platform-node:latest"

// renderCompose builds the file. Pure and separately tested: every value in it
// is interpolated into YAML, and a value that breaks out of its quotes changes
// what the file declares.
func renderCompose(req DeployRequest) (string, error) {
	if !nodeIDPattern.MatchString(req.NodeID) {
		return "", fmt.Errorf("node id must be 2-63 characters of letters, digits, dot, dash or underscore")
	}
	if !hostPortPattern.MatchString(req.CoreGRPCAddr) {
		return "", fmt.Errorf("the Core gRPC address must be host:port, with no scheme and no path")
	}
	// The token is hex from Core and the fingerprint is hex or colon-separated
	// hex. Both are checked rather than trusted, because they arrive through the
	// UI and land inside quotes in a file that configures a privileged container.
	if req.EnrollToken == "" || strings.ContainsAny(req.EnrollToken, "\"'\n\r\\$") {
		return "", fmt.Errorf("the enroll token is missing or contains characters it cannot contain")
	}
	fingerprint := ""
	if fp := strings.TrimSpace(req.TLSFingerprint); fp != "" {
		if strings.ContainsAny(fp, "\"'\n\r\\$") {
			return "", fmt.Errorf("the TLS fingerprint contains characters it cannot contain")
		}
		// Only emitted when there is one to pin: an empty GRPC_TLS_FINGERPRINT
		// reads as "pin nothing" on some paths and "pin the empty string" on
		// others, and neither is what an operator without TLS meant.
		fingerprint = fmt.Sprintf("      GRPC_TLS_FINGERPRINT: %q\n", fp)
	}
	return fmt.Sprintf(composeTemplate,
		defaultNodeImage, req.NodeID, req.EnrollToken, req.CoreGRPCAddr, fingerprint), nil
}

// DeployNodeHere writes the compose file and, when asked, starts it.
//
// Shell-token gated: it writes to the filesystem and starts a privileged
// container. See the file comment for why that gate is the reason this is not
// callable from the proxied panel.
func (a *App) DeployNodeHere(token string, req DeployRequest) *DeployResult {
	if !a.checkShellToken(token) {
		return &DeployResult{Error: "unauthorized"}
	}

	content, err := renderCompose(req)
	if err != nil {
		return &DeployResult{Error: err.Error()}
	}

	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir, err = defaultDeployDir()
		if err != nil {
			return &DeployResult{Error: "could not find a place to write the compose file: " + err.Error()}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &DeployResult{Error: "could not create " + dir + ": " + err.Error()}
	}
	path := filepath.Join(dir, "docker-compose.yml")

	// 0600, not 0644. The file holds a pairing token, and on a shared machine
	// the default would let any other account read it before it is spent.
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return &DeployResult{Error: "could not write " + path + ": " + err.Error()}
	}
	res := &DeployResult{Ok: true, ComposePath: path}
	if !req.Start {
		return res
	}

	// Longer than a probe: this pulls an image. Still bounded, because a pull
	// against an unreachable registry otherwise hangs the button forever with
	// nothing on screen.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "compose", "-f", path, "up", "-d").CombinedOutput()
	res.Log = strings.TrimSpace(string(out))
	if err != nil {
		res.Ok = false
		res.Error = "docker compose did not start the node: " + err.Error()
		return res
	}
	res.Started = true
	return res
}

// defaultDeployDir is where the file goes when the user names no directory: a
// "node" folder beside the app's own settings, which is a path this app already
// knows it can write on every platform.
func defaultDeployDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "DylarisBeam", "node"), nil
}

// PrepareDeploy mints the enroll token and reports what the compose file will
// say, without writing anything.
//
// Split from DeployNodeHere so the screen can show the values before the user
// commits, and so a refusal from Core ("BYON is not enabled", "node limit
// reached") arrives before a file exists rather than after. Shell-token gated
// even though it writes nothing: it CONSUMES a node slot at Core, which is a
// side effect on the tenant's account.
func (a *App) PrepareDeploy(token, label string) *PreparedDeploy {
	if !a.checkShellToken(token) {
		return &PreparedDeploy{Error: "unauthorized"}
	}
	client := a.getClient()
	if client == nil {
		return &PreparedDeploy{Error: "Sign in to the panel first - the token is minted with your account."}
	}
	t, err := client.NodeEnrollToken(label)
	if err != nil {
		return &PreparedDeploy{Error: err.Error()}
	}
	dir, derr := defaultDeployDir()
	if derr != nil {
		dir = ""
	}
	return &PreparedDeploy{
		Ok:             true,
		EnrollToken:    t.Token,
		TLSFingerprint: t.TLSFingerprint,
		SuggestedDir:   dir,
	}
}

// PreparedDeploy is what the screen shows before anything is written.
type PreparedDeploy struct {
	Ok             bool   `json:"ok"`
	EnrollToken    string `json:"enrollToken,omitempty"`
	TLSFingerprint string `json:"tlsFingerprint,omitempty"`
	SuggestedDir   string `json:"suggestedDir,omitempty"`
	Error          string `json:"error,omitempty"`
}
