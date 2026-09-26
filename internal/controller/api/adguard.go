package api

type AdGuardConnectParams struct {
	Endpoint string `json:"endpoint"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type AdGuardCollectParams struct {
	ScopeID string `json:"scope_id"`
}

// AdGuardCollection reports only bounded counts and limitations. Private query
// names and client addresses stay in the local evidence store.
type AdGuardCollection struct {
	ScopeID                string `json:"scope_id"`
	QueryLogEnabled        bool   `json:"query_log_enabled"`
	Read                   int    `json:"read"`
	Inserted               int    `json:"inserted"`
	Deduplicated           int    `json:"deduplicated"`
	SkippedOutsideScope    int    `json:"skipped_outside_scope"`
	SkippedWithoutClientIP int    `json:"skipped_without_client_ip"`
	SkippedOutsideWindow   int    `json:"skipped_outside_window"`
	LimitReached           bool   `json:"limit_reached"`
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
