package mailer

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig is the resolved SMTP configuration for one purpose
// (e.g. "auth"). Loaded from settings by LoadConfig.
type SMTPConfig struct {
	Host       string
	Port       int
	Username   string
	Password   string
	FromEmail  string
	FromName   string
	Encryption string // "none" | "starttls" | "tls"
}

// SettingsReader is the slice of the Store interface that the mailer needs.
// Kept narrow so tests can fake it without dragging the full Store in.
type SettingsReader interface {
	GetSetting(key string) (string, error)
}

// LoadConfig resolves the SMTP config for a given purpose. Per-purpose keys
// (smtp.<purpose>.host etc.) win; missing fields fall back to smtp.default.*
// — that's how a deployment can override one purpose (e.g. ticket replies
// from "support@…") without re-typing every field.
func LoadConfig(s SettingsReader, purpose string) (*SMTPConfig, error) {
	if purpose == "" {
		purpose = "default"
	}

	getWithFallback := func(field string) string {
		if purpose != "default" {
			if v, _ := s.GetSetting("smtp." + purpose + "." + field); v != "" {
				return v
			}
		}
		v, _ := s.GetSetting("smtp.default." + field)
		return v
	}

	cfg := &SMTPConfig{
		Host:       strings.TrimSpace(getWithFallback("host")),
		Username:   strings.TrimSpace(getWithFallback("username")),
		Password:   getWithFallback("password"),
		FromEmail:  strings.TrimSpace(getWithFallback("from_email")),
		FromName:   strings.TrimSpace(getWithFallback("from_name")),
		Encryption: strings.ToLower(strings.TrimSpace(getWithFallback("encryption"))),
	}
	if cfg.Encryption == "" {
		cfg.Encryption = "starttls"
	}
	if portStr := strings.TrimSpace(getWithFallback("port")); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			cfg.Port = p
		}
	}
	if cfg.Port == 0 {
		// Sensible defaults per encryption mode.
		switch cfg.Encryption {
		case "tls":
			cfg.Port = 465
		default:
			cfg.Port = 587
		}
	}

	if cfg.Host == "" {
		return nil, errors.New("smtp host not configured")
	}
	if cfg.FromEmail == "" {
		return nil, errors.New("smtp from_email not configured")
	}
	return cfg, nil
}

// Message is one outgoing email. Plain-text body for now — HTML can be
// layered on later by tagging Body with MIME parts.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Send blocks until the SMTP exchange completes or fails. Keep it
// synchronous so callers can decide whether to fire-and-forget in a goroutine
// or surface the error (e.g. test-send button in admin UI).
func Send(cfg *SMTPConfig, msg Message) error {
	if cfg == nil {
		return errors.New("nil smtp config")
	}
	if msg.To == "" {
		return errors.New("empty recipient")
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	from := cfg.FromEmail
	fromHeader := from
	if cfg.FromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", cfg.FromName, from)
	}

	headers := map[string]string{
		"From":         fromHeader,
		"To":           msg.To,
		"Subject":      msg.Subject,
		"MIME-Version": "1.0",
		"Content-Type": `text/plain; charset="utf-8"`,
		"Date":         time.Now().UTC().Format(time.RFC1123Z),
	}
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	raw := []byte(b.String())

	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}

	switch cfg.Encryption {
	case "tls":
		// Implicit TLS — direct TLS connection on (usually) port 465.
		tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("tls dial: %w", err)
		}
		defer conn.Close()
		client, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("smtp client: %w", err)
		}
		defer client.Quit()
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
		return sendDATA(client, from, msg.To, raw)

	case "none":
		// Plaintext — only acceptable on trusted private networks (vRack etc).
		client, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		defer client.Quit()
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
		return sendDATA(client, from, msg.To, raw)

	default: // "starttls"
		client, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		defer client.Quit()
		tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
		if auth != nil {
			if err := client.Auth(auth); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}
		return sendDATA(client, from, msg.To, raw)
	}
}

func sendDATA(client *smtp.Client, from, to string, body []byte) error {
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := wc.Write(body); err != nil {
		wc.Close()
		return fmt.Errorf("write body: %w", err)
	}
	return wc.Close()
}
