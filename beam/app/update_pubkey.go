package main

// updatePublicKeyB64 is the base64 (std encoding) Ed25519 PUBLIC key that signs
// the beam update manifest. Public keys are safe to commit.
//
// This is the REAL signing key. The matching private seed lives ONLY in the CI
// secret BEAM_UPDATE_PRIVKEY (generated once via `go run ./cmd/beam-release
// keygen`); beam-release.yml signs latest.json with it. This value MUST stay
// byte-identical to Core's copy in core/handlers/beam_manifest.go. If it is ever
// emptied, corrupted, or drifts from Core's copy, manifest verification FAILS
// CLOSED and no update is ever surfaced.
const updatePublicKeyB64 = "5WS1g2Ushib55CKq7duxHJlcJKIwD7L3wYdA6coTiA4="
