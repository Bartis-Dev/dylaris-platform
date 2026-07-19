package modpack

import (
	"context"
	"errors"
	"testing"
)

// disableAWSRetries forces the AWS SDK to attempt exactly once instead of its
// default 3-attempt retry-with-backoff. It mirrors the helper of the same name
// in package handlers; the two are independent copies because neither package
// exports test helpers to the other.
func disableAWSRetries(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_MAX_ATTEMPTS", "1")
}

// newUnreachableS3 builds an S3Provider aimed at a loopback port nothing
// listens on. Port 1 refuses the connection instantly (no DNS lookup, no
// listener), so a call that DOES reach the network fails fast with a dial
// error - which is exactly what distinguishes it from a context error.
func newUnreachableS3(t *testing.T) *S3Provider {
	t.Helper()
	disableAWSRetries(t)
	p, err := NewS3("http://127.0.0.1:1", "us-east-1", "bucket1", "key1", "secret1")
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return p
}

// TestS3Provider_CancelledContextIsHonoured pins that the ctx handed to each
// method actually reaches the AWS SDK rather than being accepted and dropped.
//
// The assertion is the ERROR IDENTITY, not merely that an error came back: an
// implementation that ignored ctx and called context.Background() would still
// fail here (nothing listens on port 1), but it would fail with a dial error,
// never with context.Canceled. That difference is the whole test.
func TestS3Provider_CancelledContextIsHonoured(t *testing.T) {
	cases := []struct {
		name string
		call func(ctx context.Context, p *S3Provider) error
	}{
		{"Put", func(ctx context.Context, p *S3Provider) error {
			return p.Put(ctx, "a/pack.mrpack", []byte("mrpack-bytes"))
		}},
		{"Get", func(ctx context.Context, p *S3Provider) error {
			_, err := p.Get(ctx, "a/pack.mrpack")
			return err
		}},
		{"Delete", func(ctx context.Context, p *S3Provider) error {
			return p.Delete(ctx, "a/pack.mrpack")
		}},
		{"Stat", func(ctx context.Context, p *S3Provider) error {
			_, _, err := p.Stat(ctx, "a/pack.mrpack")
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newUnreachableS3(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			err := c.call(ctx, p)
			if err == nil {
				t.Fatalf("%s(cancelled ctx) = nil, want an error", c.name)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("%s(cancelled ctx) err = %v, want it to carry context.Canceled; a non-context error means ctx never reached the SDK", c.name, err)
			}
		})
	}
}

// TestS3Provider_LiveContextReachesTheNetwork is the control for the test
// above. Without it, an implementation that returned a context error
// unconditionally would pass. A usable ctx must produce a dial failure, not a
// context failure.
func TestS3Provider_LiveContextReachesTheNetwork(t *testing.T) {
	p := newUnreachableS3(t)
	err := p.Put(context.Background(), "a/pack.mrpack", []byte("mrpack-bytes"))
	if err == nil {
		t.Fatal("Put(live ctx) = nil, want a dial error (nothing listens on 127.0.0.1:1)")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Put(live ctx) err = %v, want a dial error, not a context error", err)
	}
}
