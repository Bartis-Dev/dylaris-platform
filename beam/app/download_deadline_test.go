package main

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	pb "dylaris-proto/beam"

	"google.golang.org/grpc/status"
)

// slowChunkStream delivers `chunks` chunks, one every `interval`, and fails the
// moment ctx is done - exactly how a real gRPC stream behaves when the call
// context expires mid-transfer. It is the shape of a healthy but slow download:
// bytes keep arriving, the transfer just takes longer than some fixed budget.
type slowChunkStream struct {
	ctx      context.Context
	chunks   int
	interval time.Duration
}

func (s *slowChunkStream) Recv() (*pb.BeamChunk, error) {
	if s.chunks == 0 {
		return nil, io.EOF
	}
	select {
	case <-s.ctx.Done():
		return nil, status.FromContextError(s.ctx.Err()).Err()
	case <-time.After(s.interval):
	}
	s.chunks--
	return &pb.BeamChunk{Data: []byte("x"), TotalSize: 64}, nil
}

// stalledChunkStream delivers nothing at all and only ever returns when ctx is
// done. A transfer that has genuinely stopped making progress.
type stalledChunkStream struct{ ctx context.Context }

func (s *stalledChunkStream) Recv() (*pb.BeamChunk, error) {
	<-s.ctx.Done()
	return nil, status.FromContextError(s.ctx.Err()).Err()
}

// TestWriteChunksToFile_SlowButProgressingDownloadCompletes is the regression
// guard for the fixed overall deadline the two beam download calls used to
// carry.
//
// DownloadFile and DownloadSelective capped the WHOLE transfer at 10 minutes.
// Nothing else on this path does: UploadStart deliberately builds a plain
// context.WithCancel ("uploads can be arbitrarily large"), and the Core REST
// download fallback deliberately uses a client with no timeout ("large files
// can take a while"). Only the beam path - the one built for multi-GB file
// transfer, and the one an admin can bandwidth-cap via the relay throttle - had
// the cap. A transfer slower than size/600s therefore died at the same byte
// every attempt, and writeChunksToFile restarts from zero, so there was no way
// through it at all.
//
// The budget here is scaled down but the shape is identical: data flowing the
// entire time, killed by a clock that does not care.
//
// TWO properties have to hold at once, and they pull in opposite directions:
//
//   - the whole transfer must outlast the idle window, or a watchdog that
//     wrongly measured TOTAL elapsed time - the exact bug this guards - would
//     still pass;
//   - each individual gap must be far below the idle window, or a busy CI runner
//     trips the watchdog and the test fails for reasons that have nothing to do
//     with the code.
//
// The original numbers (20ms chunks, 100ms window) satisfied the first with a
// margin of only 5x on the second, and CI failed on it twice in one day -
// including on a commit that changed nothing but a line of JSON. Widening the
// window alone would have destroyed the first property, so the chunk count grew
// with it: 100 chunks at 5ms is ~500ms of transfer against a 300ms window, which
// keeps "total > window" while needing a 60x single-chunk stall to trip.
//
// It stays robust when the runner is slow, which is when it used to break: drift
// inflates every gap AND the total, but only the total is multiplied by the
// chunk count, so "total > window" becomes MORE true as the machine gets slower.
func TestWriteChunksToFile_SlowButProgressingDownloadCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chunkGap   = 5 * time.Millisecond
		chunks     = 100
		idleWindow = 300 * time.Millisecond
	)
	if chunks*chunkGap <= idleWindow {
		t.Fatalf("the transfer (%v) must outlast the idle window (%v), or a total-elapsed watchdog would pass this test",
			chunks*chunkGap, idleWindow)
	}

	stream := &slowChunkStream{ctx: ctx, chunks: chunks, interval: chunkGap}
	dest := filepath.Join(t.TempDir(), "slow.bin")

	if err := writeChunksToFile(cancel, idleWindow, stream, dest, nil); err != nil {
		t.Fatalf("a download that never stopped making progress was torn down: %v", err)
	}
}

// TestWriteChunksToFile_StalledDownloadIsTornDown proves the idle watchdog is
// load-bearing: with no bytes at all, the download must not hang forever now
// that the overall deadline is gone.
func TestWriteChunksToFile_StalledDownloadIsTornDown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := &stalledChunkStream{ctx: ctx}
	dest := filepath.Join(t.TempDir(), "stalled.bin")

	done := make(chan error, 1)
	go func() { done <- writeChunksToFile(cancel, 60*time.Millisecond, stream, dest, nil) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled download returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled download was never torn down")
	}
}
