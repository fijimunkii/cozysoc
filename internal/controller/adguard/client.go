// Package adguard reads observations from an externally owned AdGuard Home.
// It does not configure, update, start, or stop that service.
package adguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

const (
	SupportedVersion = "v0.107.79"
	maxResponseBytes = 2 << 20
	maxQueries       = 100
	requestTimeout   = 5 * time.Second
)

var (
	ErrEndpoint    = errors.New("invalid AdGuard Home endpoint")
	ErrUnavailable = errors.New("AdGuard Home read unavailable")
	ErrAuth        = errors.New("AdGuard Home authentication failed")
	ErrVersion     = errors.New("unsupported AdGuard Home version")
	ErrResponse    = errors.New("invalid AdGuard Home response")
)

// Client only sends GET requests to fixed paths on one explicitly selected origin.
type Client struct {
	origin   string
	user     string
	password secretstore.Secret
	http     *http.Client
}

// NewClient accepts IP-literal HTTPS origins and loopback HTTP for a local
// instance. Hostname endpoints require a separately tested DNS pinning and
// reapproval path. Credentials must come from the controller's protected secret
// store; callers must not log them.
func NewClient(endpoint, user string, password secretstore.Secret) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrEndpoint
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, ErrEndpoint
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || (u.Scheme == "http" && !ip.IsLoopback()) {
		return nil, ErrEndpoint
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, ErrEndpoint
		}
	}
	if len(user) > 128 || strings.ContainsRune(user, ':') || strings.IndexFunc(user, unicode.IsControl) >= 0 || password.Len() > 1024 || (user == "") != (password.Len() == 0) {
		return nil, ErrEndpoint
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{
		origin: strings.TrimSuffix(u.String(), "/"), user: user, password: password,
		http: &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

type Status struct {
	Version           string
	Running           bool
	ProtectionEnabled bool
	FilteringEnabled  bool
	QueryLogEnabled   bool
	AnonymizedClients bool
}

// Query is a transient observation. Names and client identifiers are private
// browsing data; consumers must apply the product's retention and redaction rules.
type Query struct {
	Time        time.Time
	Name        string
	Type        string
	ClientIP    string
	ClientID    string
	Status      string
	Reason      string
	Filtering   string // blocked, not-blocked, or unknown
	Attribution string // client-ip, client-id, or unknown
}

type Snapshot struct {
	Status  Status
	Queries []Query
}

type queryItem struct {
	Time     *string `json:"time"`
	Client   string  `json:"client"`
	ClientID string  `json:"client_id"`
	Question struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"question"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Probe validates the external instance and reads only service state. It never
// fetches query history or starts collection.
func (c *Client) Probe(ctx context.Context) (Status, error) {
	var status struct {
		Version           *string `json:"version"`
		Running           *bool   `json:"running"`
		ProtectionEnabled *bool   `json:"protection_enabled"`
	}
	if err := c.get(ctx, "/control/status", &status); err != nil {
		return Status{}, err
	}
	if status.Version == nil || status.Running == nil || status.ProtectionEnabled == nil {
		return Status{}, ErrResponse
	}
	if *status.Version != SupportedVersion {
		return Status{}, ErrVersion
	}
	result := Status{Version: *status.Version, Running: *status.Running, ProtectionEnabled: *status.ProtectionEnabled}
	var filtering struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.get(ctx, "/control/filtering/status", &filtering); err != nil {
		return Status{}, err
	}
	if filtering.Enabled == nil {
		return Status{}, ErrResponse
	}
	result.FilteringEnabled = *filtering.Enabled
	var logConfig struct {
		Enabled    *bool `json:"enabled"`
		Anonymized *bool `json:"anonymize_client_ip"`
	}
	if err := c.get(ctx, "/control/querylog/config", &logConfig); err != nil {
		return Status{}, err
	}
	if logConfig.Enabled == nil || logConfig.Anonymized == nil {
		return Status{}, ErrResponse
	}
	result.QueryLogEnabled = *logConfig.Enabled
	result.AnonymizedClients = *logConfig.Anonymized
	return result, nil
}

// Read gathers one bounded, read-only snapshot. Query-log reads are skipped when
// the external owner has disabled logging. No successful HTTP status is treated as
// evidence that every household client uses this resolver.
func (c *Client) Read(ctx context.Context) (Snapshot, error) {
	status, err := c.Probe(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Status: status}
	if !status.Running || !status.QueryLogEnabled {
		return result, nil
	}
	var log struct {
		Data *[]queryItem `json:"data"`
	}
	if err := c.get(ctx, "/control/querylog?limit=100", &log); err != nil {
		return Snapshot{}, err
	}
	if log.Data == nil || len(*log.Data) > maxQueries {
		return Snapshot{}, ErrResponse
	}
	result.Queries = make([]Query, 0, len(*log.Data))
	for _, item := range *log.Data {
		if item.Time == nil || !validQuestionName(item.Question.Name) || !validToken(item.Question.Type, 16) || (item.Status != "" && !validToken(item.Status, 32)) || !validClientID(item.ClientID) {
			return Snapshot{}, ErrResponse
		}
		when, err := time.Parse(time.RFC3339Nano, *item.Time)
		if err != nil || len(item.Client) > 64 {
			return Snapshot{}, ErrResponse
		}
		if item.Client != "" {
			if _, err := netip.ParseAddr(item.Client); err != nil {
				return Snapshot{}, ErrResponse
			}
		}
		filtering, valid := filteringReason(item.Reason)
		if !valid {
			return Snapshot{}, ErrResponse
		}
		clientIP := item.Client
		if result.Status.AnonymizedClients {
			clientIP = ""
		}
		q := Query{Time: when, Name: item.Question.Name, Type: item.Question.Type, ClientIP: clientIP, ClientID: item.ClientID, Status: item.Status, Reason: item.Reason, Filtering: filtering, Attribution: "unknown"}
		if item.ClientID != "" {
			q.Attribution = "client-id"
		} else if item.Client != "" && !result.Status.AnonymizedClients {
			q.Attribution = "client-ip"
		}
		result.Queries = append(result.Queries, q)
	}
	return result, nil
}

func validQuestionName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	name = strings.TrimSuffix(name, ".")
	if name == "" {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

func validToken(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func validClientID(value string) bool {
	if len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

func filteringReason(reason string) (string, bool) {
	switch reason {
	case "", "NotFilteredError":
		return "unknown", true
	case "NotFilteredNotFound", "NotFilteredWhiteList", "Rewrite", "RewriteEtcHosts", "RewriteRule":
		return "not-blocked", true
	case "FilteredBlackList", "FilteredSafeBrowsing", "FilteredParental", "FilteredInvalid", "FilteredSafeSearch", "FilteredBlockedService":
		return "blocked", true
	default:
		return "", false
	}
}

func (c *Client) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return ErrEndpoint
	}
	req.Header.Set("Accept", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, string(c.password.Bytes()))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return ErrResponse
	}
	if resp.ContentLength > maxResponseBytes {
		return ErrResponse
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(b) > maxResponseBytes {
		return ErrResponse
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	if err := decoder.Decode(dst); err != nil {
		return ErrResponse
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrResponse
	}
	return nil
}
