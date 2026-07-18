package handlers

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestAttachmentAllowed(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	pdf := []byte("%PDF-1.7\n...")
	txt := []byte("just some log text\n")
	elf := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	script := []byte("#!/bin/sh\nrm -rf /\n")

	cases := []struct {
		name     string
		filename string
		head     []byte
		wantErr  bool
	}{
		{"png accepted", "logo.png", png, false},
		{"pdf accepted", "report.pdf", pdf, false},
		{"log accepted as text", "server.log", txt, false},
		{"txt accepted", "notes.txt", txt, false},
		{"executable extension rejected", "evil.exe", elf, true},
		{"disallowed extension rejected even with image header", "evil.sh", png, true},
		{"extension-vs-sniff mismatch rejected: .png but ELF bytes", "sneaky.png", elf, true},
		{"no extension rejected", "README", txt, true},
		// Extra case beyond the brief's minimum: a shell script disguised
		// under an allowed image extension. DetectContentType sniffs plain
		// ASCII script text as "text/plain", which does not satisfy the
		// "image/" family the .png extension requires, so this must be
		// rejected by the same extension-vs-sniff mismatch path as the ELF
		// case above.
		{"shell script disguised as .png rejected", "evil.png", script, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := attachmentAllowed(c.filename, c.head)
			if (err != nil) != c.wantErr {
				t.Fatalf("attachmentAllowed(%q) err = %v, wantErr %v", c.filename, err, c.wantErr)
			}
		})
	}
}

// fakeScanner implements AttachmentScanner for the hook test. Local to file.
type fakeScanner struct{ err error }

func (f fakeScanner) Scan(r io.Reader) error {
	_, _ = io.Copy(io.Discard, r)
	return f.err
}

func TestAttachmentScannerHook(t *testing.T) {
	clean := fakeScanner{err: nil}
	if err := clean.Scan(strings.NewReader("data")); err != nil {
		t.Fatalf("clean scan err = %v, want nil", err)
	}
	hit := fakeScanner{err: errors.New("Eicar-Test-Signature FOUND")}
	if err := hit.Scan(strings.NewReader("data")); err == nil {
		t.Fatal("infected scan err = nil, want a rejection error")
	}
}

// TestNewAttachmentScanner pins the off-by-default contract: unset
// CLAMAV_ADDR returns a nil AttachmentScanner (UploadAttachment's
// `if h.scanner != nil` gate then skips scanning entirely - no dial, no
// latency, no failure mode). Setting it returns a configured *clamdScanner.
func TestNewAttachmentScanner(t *testing.T) {
	t.Run("unset returns nil", func(t *testing.T) {
		t.Setenv("CLAMAV_ADDR", "")
		if s := newAttachmentScanner(); s != nil {
			t.Fatalf("newAttachmentScanner() = %v, want nil when CLAMAV_ADDR is unset", s)
		}
	})

	t.Run("set returns a configured scanner", func(t *testing.T) {
		t.Setenv("CLAMAV_ADDR", "127.0.0.1:3310")
		s := newAttachmentScanner()
		if s == nil {
			t.Fatal("newAttachmentScanner() = nil, want a configured scanner")
		}
		cs, ok := s.(*clamdScanner)
		if !ok {
			t.Fatalf("newAttachmentScanner() = %T, want *clamdScanner", s)
		}
		if cs.addr != "127.0.0.1:3310" {
			t.Fatalf("clamdScanner.addr = %q, want %q", cs.addr, "127.0.0.1:3310")
		}
	})
}
