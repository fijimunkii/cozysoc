// Package httpsplan constructs immutable, explicitly disclosed HTTPS review
// plans. It performs no I/O and grants no execution authority or route assurance.
package httpsplan

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/checkbinding"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

const (
	Profile               = "selected-https-v1"
	ReviewLifetime        = 30 * time.Second
	MaxRequestTargetBytes = 1024
	ExactEndpoint         = "exact-endpoint"
	UserAgent             = "CozySOC-Network-Check/1"
)

var (
	ErrConfiguration = errors.New("HTTPS review configuration is invalid")
	ErrBinding       = errors.New("HTTPS review enrollment or source binding is invalid")
	ErrClock         = errors.New("HTTPS review time is invalid")
	ErrPlan          = errors.New("HTTPS review plan is invalid")
)

type Binding struct {
	Observer nq.Observer
	Prefixes []string
	Source   netip.Addr
}

// Configuration is explicit input, not execution authority. The selected endpoint
// is a numeric address on port 443; ServerName supplies the TLS identity and HTTP
// Host independently, without DNS resolution. RequestTarget is origin-form.
// References must rotate when pinned execution-relevant values change.
type Configuration struct {
	Selection         nq.HTTPSSelection
	Endpoint          netip.AddrPort
	ServerName        string
	RequestTarget     string
	DestinationPolicy string
}

func (Configuration) String() string               { return "[HTTPS configuration]" }
func (Configuration) GoString() string             { return "[HTTPS configuration]" }
func (Configuration) MarshalJSON() ([]byte, error) { return json.Marshal("[HTTPS configuration]") }

// DisclosedConfiguration is intentionally private local review data. Only an
// explicit disclosure converts to this serializable copy; never log or place it
// in general diagnostics, normalized observations, telemetry or browser APIs.
type DisclosedConfiguration Configuration

// Policy describes required behavior of a future executor, not proof it happened.
// No caller can toggle these values inside a Plan. TLS session reuse, proxies,
// name lookups, redirects, early data and response-body reading are disabled.
type Policy struct {
	ALPN                         string
	TrustStore                   string
	ClientAuthentication         bool
	HTTPVersion                  string
	MinTLSVersion, MaxTLSVersion uint16
	VerifyServerIdentity         bool
	FreshConnection              bool
	SessionResumption            bool
	EarlyData                    bool
	UseProxy                     bool
	ResolveNames                 bool
	FollowRedirects              bool
	ReadResponseBody             bool
}

// Budget bounds one future exchange. Transport bytes count TLS records passed
// to/from the TCP stream, including handshake and buffered response data. They
// exclude TCP retransmissions and IP/link overhead and are not a billing estimate.
// Header bytes cover all response headers, including informational responses.
type Budget struct {
	MaxConnections, MaxRequests, MaxRetries                                  int
	MaxRequestBytes, MaxResponseHeaderBytes                                  int
	MaxTransportReadBytes, MaxTransportWriteBytes                            int
	MaxTransportReadCalls, MaxTransportWriteCalls                            int
	ConnectTimeout, TLSHandshakeTimeout, ResponseHeaderTimeout, TotalTimeout time.Duration
	MaxConcurrentRuns                                                        int
	MinRunInterval                                                           time.Duration
}
type Disclosure struct {
	Profile                 string
	Binding                 Binding
	Configuration           DisclosedConfiguration
	OutsideEnrolledPrefixes bool // Membership only; not proof of an external or on-link route.
	Policy                  Policy
	Budget                  Budget
	CreatedAt, ExpiresAt    time.Time
	Privacy                 []string
}
type Plan struct{ review Disclosure }

func (Plan) String() string               { return "[HTTPS review plan]" }
func (Plan) GoString() string             { return "[HTTPS review plan]" }
func (Plan) MarshalJSON() ([]byte, error) { return json.Marshal("[HTTPS review plan]") }
func (*Plan) UnmarshalJSON([]byte) error  { return ErrPlan }
func validTime(t time.Time) bool          { return !t.IsZero() && time.Unix(0, t.UnixNano()).Equal(t) }

// ValidateConfiguration is pure and selects no defaults. Noncanonical names are
// limited to ASCII case normalization; IDNA mapping, trailing-dot aliases, IP TLS
// identities, non-443 ports, URL authorities and arbitrary headers are excluded.
func ValidateConfiguration(c Configuration) error {
	if nq.ValidateHTTPSSelection(c.Selection) != nil || c.DestinationPolicy != ExactEndpoint || !c.Endpoint.IsValid() || c.Endpoint.Port() != 443 || !checkbinding.EligibleHost(c.Endpoint.Addr()) ||
		(c.Endpoint.Addr().Is4() && c.Selection.Family != nq.FamilyIPv4) || (c.Endpoint.Addr().Is6() && c.Selection.Family != nq.FamilyIPv6) || !validServerName(c.ServerName) || !validRequestTarget(c.RequestTarget) {
		return ErrConfiguration
	}
	return nil
}
func validServerName(name string) bool {
	if len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	if _, err := netip.ParseAddr(name); err == nil {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range []byte(label) {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	return true
}
func validRequestTarget(target string) bool {
	if len(target) == 0 || len(target) > MaxRequestTargetBytes || target[0] != '/' || strings.HasPrefix(target, "//") {
		return false
	}
	for _, ch := range []byte(target) {
		// RFC 3986 path/query characters only; require other bytes to be
		// explicitly escaped so the standard writer cannot rewrite the review.
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || strings.ContainsRune("-._~!$&'()*+,;=:@/?%", rune(ch))) {
			return false
		}
	}
	parsed, err := url.ParseRequestURI(target)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || parsed.RequestURI() != target {
		return false
	}
	// Reject encoded control characters and backslashes too. Validate the raw
	// query's percent escapes, which ParseRequestURI otherwise leaves unchecked.
	decoded, err := url.PathUnescape(target)
	if err != nil {
		return false
	}
	for _, ch := range []byte(decoded) {
		if ch < 32 || ch == 127 || ch == '\\' {
			return false
		}
	}
	return true
}

// requestBytes uses Go's standard HTTP writer; it does not construct a client,
// consult proxy settings, resolve a name or open a connection. The fixed headers
// are part of this profile and contain no cookies, credentials or host identity.
func requestBytes(c Configuration) ([]byte, error) {
	r, err := http.NewRequest(c.Selection.Method, "https://"+c.ServerName+c.RequestTarget, nil)
	if err != nil {
		return nil, ErrConfiguration
	}
	r.Close = true
	r.Header.Set("User-Agent", UserAgent)
	r.Header.Set("Accept", "*/*")
	r.Header.Set("Accept-Encoding", "identity")
	r.Header.Set("Cache-Control", "no-store")
	var b bytes.Buffer
	if r.Write(&b) != nil {
		return nil, ErrConfiguration
	}
	return b.Bytes(), nil
}

func New(binding Binding, c Configuration, now time.Time) (Plan, error) {
	now = now.Round(0).UTC()
	if !validTime(now) || !validTime(now.Add(ReviewLifetime)) {
		return Plan{}, ErrClock
	}
	if err := ValidateConfiguration(c); err != nil {
		return Plan{}, err
	}
	if nq.ValidateHTTPSSnapshot(nq.HTTPSSnapshot{Observer: binding.Observer, AsOf: now, WindowStart: now, Freshness: time.Second}) != nil {
		return Plan{}, ErrBinding
	}
	prefixes, member, err := checkbinding.Validate(binding.Source, c.Endpoint.Addr(), binding.Prefixes)
	if err != nil {
		return Plan{}, ErrBinding
	}
	binding.Prefixes = prefixes
	c.ServerName = strings.ToLower(c.ServerName)
	request, err := requestBytes(c)
	if err != nil {
		return Plan{}, err
	}
	return Plan{review: Disclosure{Profile: Profile, Binding: binding, Configuration: DisclosedConfiguration(c), OutsideEnrolledPrefixes: !member,
		CreatedAt: now, ExpiresAt: now.Add(ReviewLifetime),
		Policy: Policy{HTTPVersion: "HTTP/1.1", ALPN: "http/1.1", TrustStore: "system", MinTLSVersion: tls.VersionTLS12, MaxTLSVersion: tls.VersionTLS13, VerifyServerIdentity: true, FreshConnection: true},
		Budget: Budget{MaxConnections: 1, MaxRequests: 1, MaxRetries: 0, MaxRequestBytes: len(request), MaxResponseHeaderBytes: 16384, MaxTransportReadBytes: 131072, MaxTransportWriteBytes: 32768, MaxTransportReadCalls: 512, MaxTransportWriteCalls: 64,
			ConnectTimeout: 2 * time.Second, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 2 * time.Second, TotalTimeout: 8 * time.Second, MaxConcurrentRuns: 1, MinRunInterval: time.Minute},
		Privacy: []string{
			"The selected operator can observe the connecting network address, timing, TLS identity and exact HTTP request. The TLS name may also be visible to network observers; encrypted ClientHello is not part of this profile.",
			"The fixed request discloses the CozySOC-Network-Check/1 user agent. No cookies, authorization headers, request body or device identifier are supplied.",
			"TLS-stream byte ceilings include handshake and buffered response data but exclude TCP retransmissions and IP/link overhead. A server may send body bytes even though this profile does not read a response body; these can consume network data.",
			"Prefix membership does not verify an external target, gateway, route or actual egress interface. A VPN, proxy interception, metered connection or changed network needs fresh binding review before separate one-shot consent.",
		}}}, nil
}

// Disclosure returns an owned copy for deliberate local display. It grants no
// authority and must not be accepted as an executable plan from a caller.
func (p Plan) Disclosure() Disclosure {
	d := p.review
	d.Binding.Prefixes = slices.Clone(d.Binding.Prefixes)
	d.Privacy = slices.Clone(d.Privacy)
	return d
}

// RequestBytes returns the exact HTTP/1.1 bytes described by this review. The
// returned buffer is owned by the caller. Possession is not permission to send.
func (p Plan) RequestBytes() ([]byte, error) {
	if p.review.Profile != Profile {
		return nil, ErrPlan
	}
	return requestBytes(Configuration(p.review.Configuration))
}
func (p Plan) Current(now time.Time) bool {
	return p.review.Profile == Profile && validTime(now) && !now.Before(p.review.CreatedAt) && now.Before(p.review.ExpiresAt)
}
func (p Plan) SameSelection(other Plan) bool {
	if p.review.Profile != Profile || other.review.Profile != Profile {
		return false
	}
	a, b := p.review, other.review
	return a.Profile == b.Profile && a.Configuration == b.Configuration && a.Binding.Observer == b.Binding.Observer && a.Binding.Source == b.Binding.Source && slices.Equal(a.Binding.Prefixes, b.Binding.Prefixes) && a.OutsideEnrolledPrefixes == b.OutsideEnrolledPrefixes && a.Policy == b.Policy && a.Budget == b.Budget
}
