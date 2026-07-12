package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CoreClient communicates with the Dylaris Core REST API.
// Uses the same endpoints as the Panel (login, files, beam config).
type CoreClient struct {
	apiURL     string
	token      string
	httpClient *http.Client
}

// ─── Types ───────────────────────────────────────────────────────────

type LoginResult struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
	Token    string `json:"token"`
}

type BeamConfig struct {
	RelayAddress string        `json:"relay_address"`
	Enabled      bool          `json:"enabled"`
	Branding     BeamBranding  `json:"branding"`
}

type BeamBranding struct {
	Name    string `json:"name"`
	LogoURL string `json:"logo_url"`
}

type BeamServer struct {
	ID              int    `json:"id"`
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	NodeID          string `json:"node_id"`
	NodeName        string `json:"node_name"`
	ActiveSubServer string `json:"active_sub_server"`
}

type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

type FileListResult struct {
	Success bool        `json:"success"`
	Files   []FileEntry `json:"files"`
	Message string      `json:"message,omitempty"`
}

type FileContentResult struct {
	Success bool   `json:"success"`
	Content string `json:"content"`
	Message string `json:"message,omitempty"`
}

// ─── Constructor ─────────────────────────────────────────────────────

func NewCoreClient(apiURL string) (*CoreClient, error) {
	// Ensure URL has /api suffix
	apiURL = strings.TrimRight(apiURL, "/")
	if !strings.HasSuffix(apiURL, "/api") {
		apiURL += "/api"
	}
	// Ensure https:// or http:// prefix
	if !strings.HasPrefix(apiURL, "http://") && !strings.HasPrefix(apiURL, "https://") {
		apiURL = "https://" + apiURL
	}

	return &CoreClient{
		apiURL: apiURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// ─── Auth ────────────────────────────────────────────────────────────

func (c *CoreClient) Login(username, password string) (*LoginResult, error) {
	body, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})

	resp, err := c.httpClient.Post(c.apiURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("connection failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
		User    struct {
			Username string `json:"username"`
			IsAdmin  bool   `json:"isAdmin"`
		} `json:"user"`
		Message string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid response: %v", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("%s", result.Message)
	}

	c.token = result.Token

	return &LoginResult{
		Username: result.User.Username,
		IsAdmin:  result.User.IsAdmin,
		Token:    result.Token,
	}, nil
}

// ─── Beam API ────────────────────────────────────────────────────────

func (c *CoreClient) GetBeamConfig() (*BeamConfig, error) {
	data, err := c.get("/beam/config")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Success      bool         `json:"success"`
		RelayAddress string       `json:"relay_address"`
		Enabled      bool         `json:"enabled"`
		Branding     BeamBranding `json:"branding"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &BeamConfig{
		RelayAddress: resp.RelayAddress,
		Enabled:      resp.Enabled,
		Branding:     resp.Branding,
	}, nil
}

func (c *CoreClient) GetBeamServers() ([]BeamServer, error) {
	data, err := c.get("/beam/servers")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Success bool         `json:"success"`
		Servers []BeamServer `json:"servers"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp.Servers, nil
}

// BeamTicket is the signed ticket plus the presence-driven direct-connect hints
// (the node's LAN IPs + public address on the pinned-TLS beam port). The hints are
// present only when Core reports no relay; the app then dials the node directly.
type BeamTicket struct {
	Ticket string
	LANIPs []string
	LANPort string
	// PublicAddr is the node's public host:port on the pinned-TLS beam port, used as
	// the direct target when no LAN IP is reachable. Empty when Core has no public IP
	// for the node (or a relay is present, so there are no hints at all).
	PublicAddr string
	// LANFingerprint is the SHA-256 (hex) of the node's deterministic LAN TLS cert.
	// The app pins it on the direct dial - encryption + MITM protection.
	LANFingerprint string
}

func (c *CoreClient) GetBeamTicket(serverUUID string) (*BeamTicket, error) {
	// GET, not POST: a CDN/WAF in front of Core was handing the desktop
	// app's POSTs an HTML challenge page. GET /beam/* goes through.
	data, err := c.get("/beam/ticket?server_uuid=" + url.QueryEscape(serverUUID))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Success  bool   `json:"success"`
		Ticket   string `json:"ticket"`
		Message  string `json:"message"`
		LANHints struct {
			IPs         []string `json:"ips"`
			PublicAddr  string   `json:"publicAddr"`
			Port        string   `json:"port"`
			Fingerprint string   `json:"fingerprint"`
		} `json:"lanHints"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	return &BeamTicket{
		Ticket:         resp.Ticket,
		LANIPs:         resp.LANHints.IPs,
		LANPort:        resp.LANHints.Port,
		PublicAddr:     resp.LANHints.PublicAddr,
		LANFingerprint: resp.LANHints.Fingerprint,
	}, nil
}

// ─── File Operations (via Core REST, same as Panel) ──────────────────

func (c *CoreClient) ListFiles(path, serverUUID string) (*FileListResult, error) {
	reqURL := "/files?path=" + url.QueryEscape(path) + "&server_uuid=" + url.QueryEscape(serverUUID)
	data, err := c.get(reqURL)
	if err != nil {
		return nil, err
	}
	var result FileListResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode file list: %w", err)
	}
	return &result, nil
}

func (c *CoreClient) GetFileContent(path, serverUUID string) (*FileContentResult, error) {
	reqURL := "/files/content?path=" + url.QueryEscape(path) + "&server_uuid=" + url.QueryEscape(serverUUID)
	data, err := c.get(reqURL)
	if err != nil {
		return nil, err
	}
	var result FileContentResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode file content: %w", err)
	}
	return &result, nil
}

func (c *CoreClient) SaveFile(path, content, serverUUID string) error {
	body, _ := json.Marshal(map[string]string{
		"path": path, "content": content, "server_uuid": serverUUID,
	})
	_, err := c.post("/files/save", body)
	return err
}

func (c *CoreClient) CreateFile(path string, isDir bool, serverUUID string) error {
	body, _ := json.Marshal(map[string]interface{}{
		"path": path, "isDir": isDir, "server_uuid": serverUUID,
	})
	_, err := c.post("/files/create", body)
	return err
}

func (c *CoreClient) DeleteFile(path, serverUUID string) error {
	body, _ := json.Marshal(map[string]string{
		"path": path, "server_uuid": serverUUID,
	})
	_, err := c.post("/files/delete", body)
	return err
}

func (c *CoreClient) RenameFile(oldPath, newPath, serverUUID string) error {
	body, _ := json.Marshal(map[string]string{
		"oldPath": oldPath, "newPath": newPath, "server_uuid": serverUUID,
	})
	_, err := c.post("/files/rename", body)
	return err
}

func (c *CoreClient) CopyFile(srcPath, dstPath, serverUUID string) error {
	body, _ := json.Marshal(map[string]string{
		"srcPath": srcPath, "dstPath": dstPath, "server_uuid": serverUUID,
	})
	_, err := c.post("/files/copy", body)
	return err
}

func (c *CoreClient) DownloadFile(path, serverUUID string) (io.ReadCloser, error) {
	reqURL := c.apiURL + "/files/download?path=" + url.QueryEscape(path) + "&server_uuid=" + url.QueryEscape(serverUUID)
	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	// No timeout — large files can take a while
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *CoreClient) SelectiveDownload(basePath string, selected []string, selectAll bool, serverUUID string) (io.ReadCloser, error) {
	reqURL := fmt.Sprintf("%s/files/download/selective?base_path=%s&server_uuid=%s&select_all=%v",
		c.apiURL, url.QueryEscape(basePath), url.QueryEscape(serverUUID), selectAll)
	for _, s := range selected {
		reqURL += "&selected=" + url.QueryEscape(s)
	}
	req, _ := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("selective download failed: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// ─── HTTP Helpers ────────────────────────────────────────────────────

func (c *CoreClient) get(path string) ([]byte, error) {
	req, _ := http.NewRequest("GET", c.apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readCoreResponse(resp)
}

func (c *CoreClient) post(path string, body []byte) ([]byte, error) {
	req, _ := http.NewRequest("POST", c.apiURL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return readCoreResponse(resp)
}

// readCoreResponse reads the body and turns any 4xx/5xx into an error
// that carries the status code and a body snippet. Without this an HTML
// error page (from a proxy/CDN, not Core) reached the JSON decoder and
// surfaced as the useless "invalid character '<'".
func readCoreResponse(resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		snippet := strings.TrimSpace(string(data))
		if len(snippet) > 160 {
			snippet = snippet[:160] + "…"
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, snippet)
	}
	return data, nil
}
