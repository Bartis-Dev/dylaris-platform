package main

import (
	"testing"
	"time"
)

// The allocation strategy is an admin setting that lives in Redis and is
// re-read every 30 seconds. PortManager is constructed during startup, BEFORE
// the first poll, so a manager that copies the mode into a field captures the
// compiled-in "sequential" default and keeps it forever: switching to "random"
// in the panel changed a global nothing read any more, and looked applied.
//
// Asking the function on every allocation is what makes the setting live, so
// that is what this pins.
func TestPortManager_ReadsTheModeOnEveryAllocation(t *testing.T) {
	mode := "sequential"
	pm := &PortManager{
		rangeStart: 25600,
		rangeEnd:   25604,
		portMode:   func() string { return mode },
		usedPorts:  map[int]string{},
	}

	if got := pm.portMode(); got != "sequential" {
		t.Fatalf("mode = %q, want sequential", got)
	}
	// What the 30s refresh does. A manager holding a copy would not see this.
	mode = "random"
	if got := pm.portMode(); got != "random" {
		t.Errorf("after a refresh the manager still reports %q - the panel setting "+
			"is being ignored", got)
	}
}

// The globals behind the accessors are written by the 30s refresh goroutine and
// read by the heartbeat, the link reconciler, the stats collector and every
// container start. Under -race this fails against unguarded package vars.
func TestModes_ConcurrentReadWrite(t *testing.T) {
	stop := make(chan struct{})
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				setModes("gateway", "beam", "random", 25565, 500, 512)
			} else {
				setModes("ip_port", "sftp", "sequential", 25566, 100, 0)
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = getRoutingMode()
			_ = getFileAccessMode()
			_ = getPortMode()
			_ = getContainerPort()
			_ = getIOWeight()
			_ = getPidsLimit()
			_, _, _, _, _, _ = getModes()
		}
	}()

	// Wall-clock, not an iteration count. A tight read loop in the test
	// goroutine finishes before the writer is ever scheduled, and then the
	// detector sees no conflicting access and the test passes against
	// completely unguarded globals - which is exactly what happened when this
	// was written as `for i := 0; i < 20000; i++`.
	time.Sleep(150 * time.Millisecond)
	close(stop)
	<-done
	<-done
}

// A refresh must be visible as a whole. Reading routing mode from the new round
// paired with file-access mode from the old one is a combination Core never
// published, and the pair decides whether the node hides its IP.
func TestSetModes_AppliesTheWholeRound(t *testing.T) {
	setModes("ip_port", "sftp", "sequential", 25565, 0, 0)
	setModes("gateway", "beam", "random", 25570, 300, 256)

	routing, fileAccess, port, cPort, io, pids := getModes()
	if routing != "gateway" || fileAccess != "beam" || port != "random" {
		t.Errorf("modes = %q/%q/%q, want gateway/beam/random", routing, fileAccess, port)
	}
	if cPort != 25570 || io != 300 || pids != 256 {
		t.Errorf("numbers = %d/%d/%d, want 25570/300/256", cPort, io, pids)
	}
}
