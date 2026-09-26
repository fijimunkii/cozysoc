package api

// OPNsenseConnectParams are transient native-IPC input. The key, secret and
// enrolled certificate are stored together only behind a protected reference.
type OPNsenseConnectParams struct {
	Endpoint  string `json:"endpoint"`
	APIKey    string `json:"api_key"`
	APISecret string `json:"api_secret"`
	TrustPEM  string `json:"trust_pem,omitempty"`
}

// OPNsenseConnection is a status-only native projection. It contains no API
// credential, trust material, router hostname, or neighbor history.
type OPNsenseConnection struct {
	Connected bool   `json:"connected"`
	Endpoint  string `json:"endpoint,omitempty"`
	Version   string `json:"version,omitempty"`
}

// OPNsenseCollectParams binds one approved read to the exact displayed router
// origin and enrolled interface. The server revalidates both before the read.
type OPNsenseCollectParams struct {
	ScopeID  string                  `json:"scope_id"`
	Expected OPNsenseCollectExpected `json:"expected"`
}

type OPNsenseCollectExpected struct {
	Endpoint  string           `json:"endpoint"`
	Interface NetworkInterface `json:"interface"`
}

// OPNsenseCollection exposes counts and limitations only. Neighbor addresses
// and hardware identifiers remain in the controller's local evidence store.
type OPNsenseCollection struct {
	ScopeID          string `json:"scope_id"`
	Read             int    `json:"read"`
	IPv4Total        int    `json:"ipv4_total"`
	IPv6Total        int    `json:"ipv6_total"`
	IPv4Truncated    bool   `json:"ipv4_truncated"`
	IPv6Truncated    bool   `json:"ipv6_truncated"`
	Inserted         int    `json:"inserted"`
	Deduplicated     int    `json:"deduplicated"`
	SkippedOutside   int    `json:"skipped_outside_scope"`
	SkippedDuplicate int    `json:"skipped_duplicate"`
}
