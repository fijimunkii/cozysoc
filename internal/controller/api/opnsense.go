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
