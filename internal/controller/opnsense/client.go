// Package opnsense reads bounded neighbor observations from an externally
// owned OPNsense router. It never changes router configuration.
package opnsense

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fijimunkii/cozysoc/internal/controller/secretstore"
)

const (
	// 26.7.4 is the exact current 26.7 API candidate. This client is not a
	// supported adapter until an owned router lab passes.
	SupportedVersion = "26.7.4"
	MaxNeighbors     = 256 // Per address family in one explicitly requested read.
	maxResponseBytes = 2 << 20
	requestTimeout   = 8 * time.Second
)

var (
	ErrEndpoint    = errors.New("invalid OPNsense endpoint")
	ErrTrust       = errors.New("invalid OPNsense certificate trust")
	ErrAuth        = errors.New("OPNsense API authentication or permission failed")
	ErrUnavailable = errors.New("OPNsense API read unavailable")
	ErrVersion     = errors.New("unsupported OPNsense version")
	ErrResponse    = errors.New("invalid OPNsense API response")
)

var interfaceName = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,32}$`)

// Client issues GETs to three fixed API paths on one private IP-literal HTTPS
// origin. The supplied key and secret must come from a protected secret store.
type Client struct {
	origin string
	key    string
	secret secretstore.Secret
	http   *http.Client
}

// NewClient trusts the OS roots plus an optional, explicitly enrolled PEM CA
// or self-signed router certificate. Ordinary hostname/IP certificate checks
// stay enabled. A hostname or untrusted certificate needs separate validation,
// never a global TLS verification bypass.
func NewClient(endpoint, key string, secret secretstore.Secret, trustPEM []byte) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.RawPath != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrEndpoint
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !(ip.IsPrivate() || ip.IsLoopback()) || ip.Zone() != "" {
		return nil, ErrEndpoint
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, ErrEndpoint
		}
	}
	if key == "" || len(key) > 128 || !utf8.ValidString(key) || strings.ContainsRune(key, ':') ||
		strings.IndexFunc(key, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 ||
		secret.Len() == 0 || secret.Len() > 1024 || len(trustPEM) > 32<<10 {
		return nil, ErrEndpoint
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if len(trustPEM) != 0 && !roots.AppendCertsFromPEM(trustPEM) {
		return nil, ErrTrust
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	return &Client{origin: strings.TrimSuffix(u.String(), "/"), key: key, secret: secret,
		http: &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}}, nil
}

type Status struct {
	Version string
}

type Neighbor struct {
	IP        netip.Addr
	MAC       string
	Interface string
	Family    string // ipv4 or ipv6
}

type Snapshot struct {
	Status        Status
	Neighbors     []Neighbor
	IPv4Total     int
	IPv6Total     int
	IPv4Truncated bool
	IPv6Truncated bool
}

// Probe validates an exact release before reading neighbor data. The response's
// router hostname is deliberately not projected.
func (c *Client) Probe(ctx context.Context) (Status, error) {
	var response struct {
		Versions []string `json:"versions"`
	}
	if err := c.get(ctx, "/api/diagnostics/system/system_information", &response); err != nil {
		return Status{}, err
	}
	if len(response.Versions) == 0 || len(response.Versions[0]) > 128 {
		return Status{}, ErrResponse
	}
	parts := strings.Fields(response.Versions[0])
	if len(parts) != 2 || parts[0] != "OPNsense" || !strings.HasPrefix(parts[1], SupportedVersion+"-") ||
		len(parts[1]) <= len(SupportedVersion)+1 || !interfaceName.MatchString(parts[1][len(SupportedVersion)+1:]) {
		return Status{}, ErrVersion
	}
	return Status{Version: SupportedVersion}, nil
}

// ReadNeighbors samples the router's ARP and NDP tables once. These are
// router-reported neighbors, not proof of current device presence, all local
// traffic, or whole-network coverage. One family failing fails the whole read.
func (c *Client) ReadNeighbors(ctx context.Context) (Snapshot, error) {
	status, err := c.Probe(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Status: status}
	for _, family := range []struct{ name, path string }{
		{"ipv4", "/api/diagnostics/interface/get_arp"},
		{"ipv6", "/api/diagnostics/interface/get_ndp"},
	} {
		var rows []json.RawMessage
		if err := c.get(ctx, family.path, &rows); err != nil {
			return Snapshot{}, err
		}
		if rows == nil {
			return Snapshot{}, ErrResponse
		}
		if family.name == "ipv4" {
			result.IPv4Total, result.IPv4Truncated = len(rows), len(rows) > MaxNeighbors
		} else {
			result.IPv6Total, result.IPv6Truncated = len(rows), len(rows) > MaxNeighbors
		}
		for i, raw := range rows {
			if i >= MaxNeighbors {
				break
			}
			var row struct {
				IP   string `json:"ip"`
				MAC  string `json:"mac"`
				Intf string `json:"intf"`
			}
			if err := json.Unmarshal(raw, &row); err != nil || !interfaceName.MatchString(row.Intf) {
				return Snapshot{}, ErrResponse
			}
			ip, err := netip.ParseAddr(row.IP)
			if err != nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Is4In6() ||
				(family.name == "ipv4") != ip.Is4() {
				return Snapshot{}, ErrResponse
			}
			mac, err := net.ParseMAC(row.MAC)
			if err != nil || len(mac) != 6 || mac[0]&1 != 0 ||
				(mac[0]|mac[1]|mac[2]|mac[3]|mac[4]|mac[5]) == 0 {
				return Snapshot{}, ErrResponse
			}
			result.Neighbors = append(result.Neighbors, Neighbor{IP: ip.WithZone(""), MAC: mac.String(), Interface: row.Intf, Family: family.name})
		}
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, path string, destination any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return ErrUnavailable
	}
	req.SetBasicAuth(c.key, string(c.secret.Bytes()))
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrAuth
	}
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return ErrResponse
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxResponseBytes || json.Unmarshal(body, destination) != nil {
		return ErrResponse
	}
	return nil
}
