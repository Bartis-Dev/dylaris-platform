// Package fileperms is the one definition of what an account may do to a
// server's FILES, shared by everything that has to agree about it.
//
// Three parties need the same answer and reach it by different routes. Core
// resolves it from the capability catalog. The SFTP sync publishes it per (node,
// user) into Redis. Core's beam handler stamps it into the ticket it signs. The
// node reads whichever of the two arrives and enforces it.
//
// It lives here, in the shared module, because the alternative was three
// structs with the same three fields in three packages, and the failure those
// produce is silent: a field renamed on one side and not the other does not stop
// compiling, it just decodes as false - which on this platform means "may do
// nothing" and takes a working session away, or, if the polarity is ever
// inverted, hands one out.
package fileperms

// Perms is one account's permissions on one server's files, in the three verbs
// the file capabilities distinguish.
//
// The JSON keys are single letters because these travel in two size-sensitive
// places: a Redis value republished for every (node, user) pair every 60
// seconds, and a JWT that is carried in a stream header.
//
// ABSENT MEANS FALSE, everywhere, deliberately. A value written by a component
// that predates these fields decodes as no permission rather than as full
// permission, so an upgrade that reaches one side first refuses work instead of
// allowing unchecked work. That direction is recoverable; the other is not.
type Perms struct {
	Read   bool `json:"r"`
	Write  bool `json:"w"`
	Delete bool `json:"d"`
}

// Full is what an owner or an administrator holds. Both short-circuit the
// resolver rather than resolving - the resolver's own owner branch returns
// everything, and an administrator is not a tenant of their own platform - so
// the short-circuit has to produce this rather than a zero value, which would
// read as "may do nothing".
func Full() Perms { return Perms{Read: true, Write: true, Delete: true} }
