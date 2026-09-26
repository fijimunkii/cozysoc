package api

type AdGuardConnectParams struct {
	Endpoint string `json:"endpoint"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// AdGuardConnection is a native-only projection. It never contains a password,
// secret reference, query name, client address, or browsing history.
type AdGuardConnection struct {
	Connected         bool   `json:"connected"`
	Endpoint          string `json:"endpoint,omitempty"`
	Username          string `json:"username,omitempty"`
	Version           string `json:"version,omitempty"`
	Running           bool   `json:"running"`
	ProtectionEnabled bool   `json:"protection_enabled"`
	FilteringEnabled  bool   `json:"filtering_enabled"`
	QueryLogEnabled   bool   `json:"query_log_enabled"`
	AnonymizedClients bool   `json:"anonymized_clients"`
}
