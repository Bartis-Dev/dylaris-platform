package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"log"

	beamauth "dylaris-pkg/beam/auth"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// coreDialCreds returns the transport-credentials dial option for every
// node->core gRPC dial. With GRPC_TLS_ENABLED off it is plaintext (today's
// behavior). With it on it is server-authenticated TLS with the Core cert
// fingerprint pinned: the cert is derived cluster-wide from CLUSTER_SECRET, so
// the node derives the same expected fingerprint locally and rejects anything
// else. There is no silent downgrade - a pin failure fails the dial.
func coreDialCreds() grpc.DialOption {
	if !grpcTLSEnabled {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}
	pinnedFP, err := beamauth.ClusterGRPCCertFingerprint(clusterSecret)
	if err != nil {
		// CLUSTER_SECRET is fatal-checked at boot, so this cannot happen in
		// practice; fail closed rather than downgrade to plaintext.
		log.Fatalf("FATAL: cannot derive core gRPC cert fingerprint: %v", err)
	}
	cfg := &tls.Config{
		InsecureSkipVerify: true, // we pin instead of CA/hostname chain-verify
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("core gRPC: no peer certificate")
			}
			got := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if hex.EncodeToString(got[:]) != pinnedFP {
				return errors.New("core gRPC: certificate fingerprint mismatch")
			}
			return nil
		},
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(cfg))
}
