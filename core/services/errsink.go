package services

import (
	"fmt"
	"log"
	"sync/atomic"

	"dylaris-pkg/errlog"
)

// Where a background failure goes so somebody learns about it.
//
// Every other component reports its failures into dylaris:errors:<service>:* and
// the panel's Errors screen reads them: edge, link, hub, beam, node and warp are
// the whole of errlog.Services. Core was not among them, and Core is what runs
// the thirty periodic services - dunning, the backup scheduler, the retention
// sweeps, the ACL reconciler, the route republisher. Their failures reached
// log.Printf and stopped there.
//
// That is worse than it sounds, because a container's stdout dies with the
// container. Measured on 2026-09-03: `docker service logs --since 720h` on the
// production Core returned 197 lines, all of them after that evening's deploy.
// So the evidence of a job that has been failing every night is destroyed by a
// redeploy - which is also the first thing anyone does when something seems
// wrong.
//
// A package-level sink rather than a SetErrorLog on each of the thirty
// services: they all live in this package and are all wired from the same few
// lines of main, so a setter each would be thirty lines of plumbing for one
// destination that is chosen once at boot. Nil until main wires it, which is
// what tests get - logErrf still writes to the log, so nothing has to know
// about this to keep working.
var errSink atomic.Pointer[errlog.Logger]

// SetErrorSink wires the background services' failures into Core's own error
// stream. Called once from main after Redis is up.
func SetErrorSink(l *errlog.Logger) { errSink.Store(l) }

// logErrf reports a background failure: to the log as before, and to Core's
// error stream so the panel can show it.
//
// source names the service, not the file ("billing-lifecycle", "backup-
// scheduler"), because that is what a reader filters the Errors screen by.
//
// ERROR only, deliberately. The streams already carry enough INFO and WARN that
// the screen is hard to read, and the point here is the failure nobody would
// otherwise hear about.
func logErrf(source, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s: %s", source, msg)
	errSink.Load().Error(source, msg)
}

// ReportOperatorError is logErrf for callers outside this package.
//
// It exists for the failures a REQUEST path swallows on purpose. The one that
// motivated it is the password reset: the endpoint answers success whether the
// mail went out or not, deliberately, so that nobody can enumerate accounts
// with it - which leaves the operator as the only party who can be told, and
// "just log for ops" was not telling anyone.
//
// The stream is capped at 500 entries and trims itself, which is what keeps a
// hammered public endpoint from pushing every other error out of the record.
func ReportOperatorError(source, format string, args ...interface{}) {
	logErrf(source, format, args...)
}
