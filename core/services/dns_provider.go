package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DNSRecord is one A record as seen/managed by a DNSProvider.
type DNSRecord struct {
	ID      string // provider-assigned id (needed to delete)
	Name    string // FQDN, e.g. "*.eu.dylaris.com"
	Type    string // always "A" here
	Content string // the IPv4 address
}

// DNSProvider is the small surface the reconciler needs from a DNS backend. Only
// A records are managed (raw Minecraft TCP/UDP needs real IPs, DNS-only).
type DNSProvider interface {
	// ListA returns the existing A records for an exact record name.
	ListA(ctx context.Context, name string) ([]DNSRecord, error)
	// CreateA creates a DNS-only (unproxied) A record name -> ip.
	CreateA(ctx context.Context, name, ip string) error
	// DeleteRecord removes a record by its provider id.
	DeleteRecord(ctx context.Context, id string) error
}

// --- Cloudflare ---

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// CloudflareProvider implements DNSProvider against the Cloudflare API for a
// single zone. The token + zone live only here (in Core), never on the edges.
type CloudflareProvider struct {
	token  string
	zoneID string
	http   *http.Client
}

// NewCloudflareProvider returns a provider, or nil when credentials are missing
// (so the caller can treat "not configured" as "feature off").
func NewCloudflareProvider(token, zoneID string) *CloudflareProvider {
	if token == "" || zoneID == "" {
		return nil
	}
	return &CloudflareProvider{
		token:  token,
		zoneID: zoneID,
		http:   &http.Client{Timeout: 10 * time.Second},
	}
}

// cfRecord is the subset of a Cloudflare DNS record we read.
type cfRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type cfListResponse struct {
	Success bool       `json:"success"`
	Errors  []cfError  `json:"errors"`
	Result  []cfRecord `json:"result"`
}

type cfWriteResponse struct {
	Success bool      `json:"success"`
	Errors  []cfError `json:"errors"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func cfErrText(errs []cfError) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	return fmt.Sprintf("%d: %s", errs[0].Code, errs[0].Message)
}

func (c *CloudflareProvider) do(ctx context.Context, method, url string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *CloudflareProvider) ListA(ctx context.Context, name string) ([]DNSRecord, error) {
	url := fmt.Sprintf("%s/zones/%s/dns_records?type=A&name=%s", cfAPIBase, c.zoneID, name)
	var lr cfListResponse
	if err := c.do(ctx, http.MethodGet, url, nil, &lr); err != nil {
		return nil, err
	}
	if !lr.Success {
		return nil, fmt.Errorf("cloudflare list: %s", cfErrText(lr.Errors))
	}
	out := make([]DNSRecord, 0, len(lr.Result))
	for _, r := range lr.Result {
		out = append(out, DNSRecord{ID: r.ID, Name: r.Name, Type: r.Type, Content: r.Content})
	}
	return out, nil
}

func (c *CloudflareProvider) CreateA(ctx context.Context, name, ip string) error {
	url := fmt.Sprintf("%s/zones/%s/dns_records", cfAPIBase, c.zoneID)
	body := map[string]any{
		"type":    "A",
		"name":    name,
		"content": ip,
		"ttl":     60,    // low TTL so failover converges quickly
		"proxied": false, // DNS-only: raw MC TCP/UDP must reach the edge directly
	}
	var wr cfWriteResponse
	if err := c.do(ctx, http.MethodPost, url, body, &wr); err != nil {
		return err
	}
	if !wr.Success {
		return fmt.Errorf("cloudflare create %s -> %s: %s", name, ip, cfErrText(wr.Errors))
	}
	return nil
}

func (c *CloudflareProvider) DeleteRecord(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/zones/%s/dns_records/%s", cfAPIBase, c.zoneID, id)
	var wr cfWriteResponse
	if err := c.do(ctx, http.MethodDelete, url, nil, &wr); err != nil {
		return err
	}
	if !wr.Success {
		return fmt.Errorf("cloudflare delete %s: %s", id, cfErrText(wr.Errors))
	}
	return nil
}
