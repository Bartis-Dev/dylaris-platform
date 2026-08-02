package main

import "sync"

// The runtime knobs Core publishes to Redis and this node re-reads every 30
// seconds: routing mode, file-access mode, port allocation strategy, the MC
// container port, and the two cgroup weights.
//
// They are package globals written by ONE goroutine (the mode-refresh ticker in
// main) and read by several others - the heartbeat, the link reconciler, the
// stats collector, and every command handler that builds a container. That is a
// data race: a string header is two words, so a torn read can pair one value's
// pointer with another's length, and the Go memory model gives no guarantee for
// the int cases either however atomic they happen to be on amd64.
//
// Guarded the same way nodeSecret already is (see redisacl_bootstrap.go), with
// an RWMutex rather than a plain one because these are read far more often than
// they are written - once every 30 seconds against every container start, every
// stats tick and every heartbeat.
//
// Go through the accessors. The globals stay unexported and untouched outside
// this file and the two setters below.
var modeMu sync.RWMutex

func getRoutingMode() string {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return routingMode
}

func getFileAccessMode() string {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return fileAccessMode
}

// getPortMode is a function rather than a value on purpose: PortManager used to
// copy the mode at construction, which is why the panel's setting never applied
// (see setModes).
func getPortMode() string {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return portMode
}

func getContainerPort() int {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return containerPort
}

func getIOWeight() uint16 {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return ioWeight
}

func getPidsLimit() int64 {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return pidsLimit
}

// setModes installs a whole refresh at once, so a reader never observes a
// half-applied round - routing mode from the new poll paired with the file
// access mode from the old one would be a combination Core never published.
func setModes(routing, fileAccess, port string, cPort int, io uint16, pids int64) {
	modeMu.Lock()
	defer modeMu.Unlock()
	routingMode, fileAccessMode, portMode = routing, fileAccess, port
	containerPort, ioWeight, pidsLimit = cPort, io, pids
}

// getModes reads the whole set for a caller that needs a consistent view, and
// for setModes' own read-modify-write: the refresh keeps the previous value of
// anything Redis does not currently carry.
func getModes() (routing, fileAccess, port string, cPort int, io uint16, pids int64) {
	modeMu.RLock()
	defer modeMu.RUnlock()
	return routingMode, fileAccessMode, portMode, containerPort, ioWeight, pidsLimit
}
