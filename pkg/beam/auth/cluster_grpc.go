package auth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
)

// Core control-channel (NodeService gRPC) TLS. Every Core derives ONE
// cluster-wide self-signed certificate from CLUSTER_SECRET alone, so every Core
// presents the identical cert and every node that holds CLUSTER_SECRET derives
// the identical SHA-256 fingerprint to pin. Domain-separated from the Beam LAN
// cert by its seed label so the two TLS channels never share a key even if their
// secrets were ever equal. Reuses zeroReader + the fixed
// lanCertNotBefore/lanCertNotAfter from lan.go (same package) so the DER - and
// thus the fingerprint - is byte-for-byte reproducible on both sides.

// DeriveClusterGRPCCert returns the cluster-wide deterministic self-signed TLS
// certificate for the node<->core control channel plus its lowercase-hex SHA-256
// fingerprint. Every Core calls this with the same CLUSTER_SECRET and gets an
// identical result.
func DeriveClusterGRPCCert(secret string) (tls.Certificate, string, error) {
	seed := sha256.Sum256([]byte("dylaris-core-grpc-tls\x00" + secret))
	priv := ed25519.NewKeyFromSeed(seed[:])

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dylaris-core-grpc"},
		NotBefore:             lanCertNotBefore,
		NotAfter:              lanCertNotAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(zeroReader{}, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	fp := sha256.Sum256(der)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	return cert, hex.EncodeToString(fp[:]), nil
}

// ClusterGRPCCertFingerprint returns just the fingerprint (the node's use - it
// never needs the private key, only the value to pin).
func ClusterGRPCCertFingerprint(secret string) (string, error) {
	_, fp, err := DeriveClusterGRPCCert(secret)
	return fp, err
}
