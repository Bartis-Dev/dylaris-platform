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
// The knob that buys robustness is the RATIO window/chunkGap, and it is not free:
// the transfer must outlast the window, so the test cannot run for less than
// ratio*chunkGap. A wider window alone therefore costs wall-clock and buys
// nothing; a SMALLER gap buys ratio at the same duration. That is the lever.
//
// This has now been widened twice. 20ms/100ms was a ratio of 5 and failed twice
// in one day. 5ms/300ms was a ratio of 60, was verified over 40 runs pinned to
// one CPU, and still lost to a single ~300ms scheduler stall on a loaded runner.
// 2ms/1s is a ratio of 500: tripping it needs a full second of no progress on a
// stream delivering every 2ms.
//
// It degrades the right way when the runner is slow, which is when it breaks:
// drift inflates every gap AND the total, but only the total is multiplied by the
// chunk count, so "total > window" becomes MORE true as the machine gets slower.
// A slow runner makes this test take longer; it must not make it fail.
//
// BOTH properties are asserted below. The previous round asserted only the first
// and left the ratio in prose - and the ratio is the half that failed.
func TestWriteChunksToFile_SlowButProgressingDownloadCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		chunkGap   = 2 * time.Millisecond
		chunks     = 600
		idleWindow = 1 * time.Second
		minRatio   = 200
	)
	if chunks*chunkGap <= idleWindow {
		t.Fatalf("the transfer (%v) must outlast the idle window (%v), or a total-elapsed watchdog would pass this test",
			chunks*chunkGap, idleWindow)
	}
	if idleWindow/chunkGap < minRatio {
		t.Fatalf("a single chunk gap only has to stretch %dx to trip the watchdog; below %dx a loaded runner decides this test",
			idleWindow/chunkGap, minRatio)
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
