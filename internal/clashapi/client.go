// Package clashapi is a tiny read-only client for sing-box's Clash-compatible
// HTTP API (experimental.clash_api). singctl enables it on loopback with a
// random secret and uses it for observability only: live connections (source
// process, full destination, outbound chain) and per-server latency for the
// urltest failover group. It imports neither sing-box nor the UI, so it builds
// and tests on every platform without the singbox tag.
package clashapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client talks to a running sing-box Clash API. BaseURL is like
// "http://127.0.0.1:9090"; Secret may be empty.
type Client struct {
	BaseURL string
	Secret  string
	HTTP    *http.Client
}

// NewClient builds a Client for a "host:port" external_controller address.
func NewClient(externalController, secret string) *Client {
	return &Client{
		BaseURL: "http://" + externalController,
		Secret:  secret,
		HTTP:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Metadata mirrors the connection metadata sing-box reports. Ports are strings
// in the wire format; SourcePortNum parses SourcePort for the /proc resolver.
type Metadata struct {
	Network         string `json:"network"`
	Type            string `json:"type"`
	SourceIP        string `json:"sourceIP"`
	DestinationIP   string `json:"destinationIP"`
	SourcePort      string `json:"sourcePort"`
	DestinationPort string `json:"destinationPort"`
	Host            string `json:"host"`
	Process         string `json:"process"`
	ProcessPath     string `json:"processPath"`
}

// Connection is one active connection from GET /connections.
type Connection struct {
	ID       string    `json:"id"`
	Metadata Metadata  `json:"metadata"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
	Chains   []string  `json:"chains"`
	Rule     string    `json:"rule"`
}

type connectionsResponse struct {
	DownloadTotal int64        `json:"downloadTotal"`
	UploadTotal   int64        `json:"uploadTotal"`
	Connections   []Connection `json:"connections"`
}

// SourcePortNum returns the numeric source port, or 0 if unparseable.
func (m Metadata) SourcePortNum() int {
	n, err := strconv.Atoi(m.SourcePort)
	if err != nil {
		return 0
	}
	return n
}

// Dest returns the human destination: host when known, else ip, with port.
func (m Metadata) Dest() string {
	h := m.Host
	if h == "" {
		h = m.DestinationIP
	}
	if m.DestinationPort != "" {
		return net.JoinHostPort(h, m.DestinationPort)
	}
	return h
}

// Source returns "ip:port" of the local client.
func (m Metadata) Source() string {
	if m.SourcePort == "" {
		return m.SourceIP
	}
	return net.JoinHostPort(m.SourceIP, m.SourcePort)
}

// Connections fetches the live connection table.
func (c *Client) Connections(ctx context.Context) ([]Connection, error) {
	var resp connectionsResponse
	if err := c.getJSON(ctx, "/connections", &resp); err != nil {
		return nil, err
	}
	return resp.Connections, nil
}

// Traffic fetches the cumulative upload/download byte counters from
// /connections. A client samples these over time and charts the per-second
// deltas (the Clash API reports running totals, not rates).
func (c *Client) Traffic(ctx context.Context) (up, down int64, err error) {
	var resp connectionsResponse
	if err := c.getJSON(ctx, "/connections", &resp); err != nil {
		return 0, 0, err
	}
	return resp.UploadTotal, resp.DownloadTotal, nil
}

// ProxyState is one entry from GET /proxies (a server or a group).
type ProxyState struct {
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Now     string         `json:"now"` // selected member (groups only)
	All     []string       `json:"all"` // member tags (groups only)
	History []DelayHistory `json:"history"`
}

// DelayHistory is one latency probe result.
type DelayHistory struct {
	Time  time.Time `json:"time"`
	Delay int       `json:"delay"` // ms; 0 means timed out / unreachable
}

// LastDelay returns the most recent probe delay in ms (0 if none).
func (p ProxyState) LastDelay() int {
	if len(p.History) == 0 {
		return 0
	}
	return p.History[len(p.History)-1].Delay
}

type proxiesResponse struct {
	Proxies map[string]ProxyState `json:"proxies"`
}

// Proxies fetches the proxy/group states (urltest selection + per-node latency).
func (c *Client) Proxies(ctx context.Context) (map[string]ProxyState, error) {
	var resp proxiesResponse
	if err := c.getJSON(ctx, "/proxies", &resp); err != nil {
		return nil, err
	}
	return resp.Proxies, nil
}

// Delay actively latency-tests a single outbound tag against testURL.
func (c *Client) Delay(ctx context.Context, tag, testURL string, timeout time.Duration) (int, error) {
	q := url.Values{}
	q.Set("url", testURL)
	q.Set("timeout", strconv.Itoa(int(timeout.Milliseconds())))
	var resp struct {
		Delay int `json:"delay"`
	}
	path := "/proxies/" + url.PathEscape(tag) + "/delay?" + q.Encode()
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return 0, err
	}
	return resp.Delay, nil
}

// SelectOutbound switches the active member of a selector group (PUT
// /proxies/{group} with body {"name":"<member>"}) — how sing-box's Clash API
// implements manual proxy selection (see
// experimental/clashapi/proxies.go → group.Selector.SelectOutbound). Used to
// pin the multi-server failover group ("proxy") to one server, or back to
// "auto" for automatic urltest selection.
func (c *Client) SelectOutbound(ctx context.Context, group, member string) error {
	body, err := json.Marshal(struct {
		Name string `json:"name"`
	}{Name: member})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+"/proxies/"+url.PathEscape(group), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("clash api PUT /proxies/%s: status %d", group, resp.StatusCode)
	}
	return nil
}

// CloseConnection closes one active connection (DELETE /connections/{id}),
// mirroring sing-box's Clash API. Used by the CONNECTIONS view's "close"
// action (F6 item 1).
func (c *Client) CloseConnection(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/connections/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("clash api DELETE /connections/%s: status %d", id, resp.StatusCode)
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	if c.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.Secret)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clash api %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
