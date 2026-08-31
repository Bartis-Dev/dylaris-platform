package main

import (
	"strings"
	"testing"
)

func validDeployRequest() DeployRequest {
	return DeployRequest{
		NodeID:       "my-node-1",
		EnrollToken:  "6f1a2b3c4d5e",
		CoreGRPCAddr: "core.example.com:25501",
	}
}

// Every field here is interpolated into a YAML file that configures a container
// with the docker socket mounted. A value that breaks out of its quotes does not
// produce a broken file - it produces a DIFFERENT one, and the machine it runs on
// belongs to a customer.
func TestRenderComposeRejectsValuesThatEscapeTheirQuotes(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  func(DeployRequest) DeployRequest
	}{
		{"a quote in the node id", func(r DeployRequest) DeployRequest {
			r.NodeID = `a" , "b`
			return r
		}},
		{"a newline in the node id", func(r DeployRequest) DeployRequest {
			r.NodeID = "a\nprivileged: true"
			return r
		}},
		{"a quote in the enroll token", func(r DeployRequest) DeployRequest {
			r.EnrollToken = `x"` + "\n" + `      privileged: "true`
			return r
		}},
		{"a newline in the enroll token", func(r DeployRequest) DeployRequest {
			r.EnrollToken = "abc\ndef"
			return r
		}},
		{"a quote in the fingerprint", func(r DeployRequest) DeployRequest {
			r.TLSFingerprint = `aa"bb`
			return r
		}},
		{"a scheme on the gRPC address", func(r DeployRequest) DeployRequest {
			r.CoreGRPCAddr = "https://core.example.com:25501"
			return r
		}},
		{"a path on the gRPC address", func(r DeployRequest) DeployRequest {
			r.CoreGRPCAddr = "core.example.com:25501/evil"
			return r
		}},
		{"an empty node id", func(r DeployRequest) DeployRequest {
			r.NodeID = ""
			return r
		}},
		{"an empty enroll token", func(r DeployRequest) DeployRequest {
			r.EnrollToken = ""
			return r
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := renderCompose(tc.req(validDeployRequest())); err == nil {
				t.Error("accepted a value that must not reach the file")
			}
		})
	}
}

func TestRenderComposeWritesWhatTheNodeNeeds(t *testing.T) {
	got, err := renderCompose(validDeployRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`NODE_ID: "my-node-1"`,
		`NODE_ENROLL_TOKEN: "6f1a2b3c4d5e"`,
		`CORE_GRPC_ADDR: "core.example.com:25501"`,
		"network_mode: host",
		defaultNodeImage,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the file is missing %q:\n%s", want, got)
		}
	}

	// The fleet credential must never be written to a machine this app runs on.
	// The node earns a scoped Redis credential by pairing; shipping the shared
	// one would undo per-node scoping entirely.
	if strings.Contains(got, "CLUSTER_SECRET") {
		t.Error("the compose file carries the cluster secret")
	}
}

// An empty fingerprint means Core runs the control channel without TLS. Writing
// the variable anyway would tell the node to pin the empty string, which reads
// as "pin nothing" on one path and "pin this" on another - and neither is what an
// operator without TLS meant.
func TestRenderComposeOmitsTheFingerprintWhenThereIsNone(t *testing.T) {
	got, err := renderCompose(validDeployRequest())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "GRPC_TLS_FINGERPRINT") {
		t.Errorf("an empty fingerprint was written anyway:\n%s", got)
	}

	req := validDeployRequest()
	req.TLSFingerprint = "AA:BB:CC"
	got, err = renderCompose(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `GRPC_TLS_FINGERPRINT: "AA:BB:CC"`) {
		t.Errorf("a real fingerprint was not written:\n%s", got)
	}
}

// The binding writes to disk and starts a privileged container, so it is gated
// like every other side-effecting one. Without the token it must refuse BEFORE
// touching anything - which is also why the deploy screen lives in the app shell
// rather than in the proxied panel, where the token deliberately does not exist.
func TestDeployNodeHereRefusesWithoutTheShellToken(t *testing.T) {
	a := &App{}
	res := a.DeployNodeHere("not-the-token", validDeployRequest())
	if res.Ok || res.Error != "unauthorized" {
		t.Errorf("DeployNodeHere = %+v, want an unauthorized refusal", res)
	}
	if res.ComposePath != "" {
		t.Error("it named a path, so it got far enough to consider writing one")
	}
}
