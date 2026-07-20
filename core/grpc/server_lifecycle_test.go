package nodegrpc

import (
	"net"
	"testing"
	"time"
)

// freePort asks the OS for an unused port and hands it back. There is a race
// between closing here and binding in the code under test, but nothing else in
// this suite binds ports, so it is not a practical source of flakes.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if err := lis.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

// startTestGRPCServer starts a server with the collaborators nilled out. None of
// them is touched until a node connects, and no test here connects one.
func startTestGRPCServer(t *testing.T, port int) (stop func(), err error) {
	t.Helper()
	srv, err := StartGRPCServer(port, NewRegistry(), nil, "core-test", nil, nil, nil, nil, false, "")
	if err != nil {
		return nil, err
	}
	return srv.Stop, nil
}

// TestStartGRPCServerReturnsAStoppableServer covers the reason the signature
// returns the server at all: without a handle, main could not drain node
// streams on shutdown and every one of them was severed by process exit.
func TestStartGRPCServerReturnsAStoppableServer(t *testing.T) {
	port := freePort(t)

	srv, err := StartGRPCServer(port, NewRegistry(), nil, "core-test", nil, nil, nil, nil, false, "")
	if err != nil {
		t.Fatalf("StartGRPCServer: %v", err)
	}
	if srv == nil {
		t.Fatal("StartGRPCServer returned a nil server with a nil error")
	}

	// With no streams open this returns promptly. The timeout is here so a
	// regression that makes it block fails the test instead of hanging it.
	stopped := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		srv.Stop()
		t.Fatal("GracefulStop did not return within 10s on an idle server")
	}
}

// TestStartGRPCServerReportsABindFailure pins the other half of the signature
// change. The bind used to happen inside a goroutine, so a port clash killed the
// process via log.Fatalf after boot had already continued past it; now it is a
// returned error at a defined point in the boot sequence.
func TestStartGRPCServerReportsABindFailure(t *testing.T) {
	port := freePort(t)

	stop, err := startTestGRPCServer(t, port)
	if err != nil {
		t.Fatalf("first StartGRPCServer: %v", err)
	}
	t.Cleanup(stop)

	if _, err := startTestGRPCServer(t, port); err == nil {
		t.Fatal("second StartGRPCServer on an occupied port = nil error, want a bind failure")
	}
}
