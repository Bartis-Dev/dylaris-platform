package mailer

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// fakeESMTP answers a greeting and an EHLO, advertising exactly the extensions
// it is given. It speaks no further SMTP: every test here is decided before the
// client would send anything past EHLO.
func fakeESMTP(t *testing.T, extensions ...string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				conn.Write([]byte("220 fake ESMTP\r\n"))
				if _, err := r.ReadString('\n'); err != nil {
					return
				}
				lines := append([]string{"SIZE 1000000"}, extensions...)
				var b strings.Builder
				for i, l := range lines {
					sep := "-"
					if i == len(lines)-1 {
						sep = " "
					}
					b.WriteString("250" + sep + l + "\r\n")
				}
				conn.Write([]byte(b.String()))
				for {
					if _, err := r.ReadString('\n'); err != nil {
						return
					}
					conn.Write([]byte("221 bye\r\n"))
				}
			}()
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port
}

// The rule this decides: "starttls" means TLS is required, not attempted.
//
// A relay that does not advertise STARTTLS used to fall through to a plaintext
// send and report success. That is the "none" mode the operator declined to
// pick, and the bodies on that wire are password-reset and email-verification
// links. Go's PlainAuth refuses a plaintext connection, so the SMTP credential
// was never what leaked - the message was.
func TestStartTLSIsRequiredWhenConfigured(t *testing.T) {
	host, port := fakeESMTP(t) // no STARTTLS advertised

	cfg := SMTPConfig{
		Host:       host,
		Port:       port,
		FromEmail:  "noreply@example.com",
		Encryption: "starttls",
	}
	err := Send(&cfg, Message{To: "someone@example.com", Subject: "s", Body: "b"})
	if err == nil {
		t.Fatal("a relay with no STARTTLS accepted the message in the clear while the config said starttls")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("error = %q, want it to name STARTTLS so the operator can act on it", err)
	}
}

// The counterpart: "none" is an explicit choice and still works. Without this,
// the fix above would read as "TLS is always required", which would strand every
// operator running a relay on a private network.
func TestPlaintextIsStillAllowedWhenChosen(t *testing.T) {
	host, port := fakeESMTP(t)

	cfg := SMTPConfig{
		Host:       host,
		Port:       port,
		FromEmail:  "noreply@example.com",
		Encryption: "none",
	}
	err := Send(&cfg, Message{To: "someone@example.com", Subject: "s", Body: "b"})
	if err != nil && strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("encryption \"none\" was refused for lack of STARTTLS: %v", err)
	}
}
