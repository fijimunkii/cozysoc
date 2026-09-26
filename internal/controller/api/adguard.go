package api

import "time"

type AdGuardConnectParams struct {
	Endpoint string `json:"endpoint"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type AdGuardCollectParams struct {
	ScopeID  string                  `json:"scope_id"`
	Expected *AdGuardCollectExpected `json:"expected,omitempty"`
}

// Expected binds a browser-approved read to the reviewed instance and scope.
// Native foreground collection can omit it because it has its own prompt.
type AdGuardCollectExpected struct {
	Endpoint  string           `json:"endpoint"`
	Interface NetworkInterface `json:"interface"`
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

// AdGuardConnection is the controller/native projection. The browser uses a
// narrower projection. Neither contains a password, secret reference, query
// name, client address, or browsing history.
type AdGuardConnection struct {
	Connected         bool                    `json:"connected"`
	Endpoint          string                  `json:"endpoint,omitempty"`
	Username          string                  `json:"username,omitempty"`
	Version           string                  `json:"version,omitempty"`
	Running           bool                    `json:"running"`
	ProtectionEnabled bool                    `json:"protection_enabled"`
	FilteringEnabled  bool                    `json:"filtering_enabled"`
	QueryLogEnabled   bool                    `json:"query_log_enabled"`
	AnonymizedClients bool                    `json:"anonymized_clients"`
	FilterInventory   *AdGuardFilterInventory `json:"filter_inventory,omitempty"`
}

type AdGuardFilterInventory struct {
	BlocklistTotal int                   `json:"blocklist_total"`
	AllowlistTotal int                   `json:"allowlist_total"`
	Truncated      bool                  `json:"truncated"`
	Sources        []AdGuardFilterSource `json:"sources"`
}

type AdGuardFilterSource struct {
	Kind        string     `json:"kind"`
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Enabled     bool       `json:"enabled"`
	RulesCount  uint32     `json:"rules_count"`
	LastUpdated *time.Time `json:"last_updated,omitempty"`
}
