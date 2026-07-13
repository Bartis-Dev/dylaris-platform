package main

// updatePublicKeyB64 is the base64 (std encoding) Ed25519 PUBLIC key that signs
// the beam update manifest. Public keys are safe to commit.
//
// PLACEHOLDER - OWNER REPLACE: run `go run ./cmd/beam-release keygen` once, store
// the printed private seed in the CI secret BEAM_UPDATE_PRIVKEY, and paste the
// printed public key here (replacing the value below).
//
// While this stays the placeholder (or is empty / not valid base64 / wrong
// length), manifest verification FAILS CLOSED and no update is ever surfaced: the
// string below is deliberately NOT valid std-base64 (the underscores are outside
// the alphabet), so DecodeString errors and verifyDetached returns false.
const updatePublicKeyB64 = "REPLACE_WITH_BASE64_ED25519_PUBLIC_KEY"
