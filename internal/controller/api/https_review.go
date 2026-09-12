package api

import "time"

type HTTPSPlan struct {
	SchemaVersion           int                `json:"schema_version"`
	Mode                    string             `json:"mode"`
	ExecutionAvailable      bool               `json:"execution_available"`
	ConsentGranted          bool               `json:"consent_granted"`
	Profile                 string             `json:"profile"`
	Configuration           HTTPSSettings      `json:"configuration"`
	Binding                 GatewayPlanBinding `json:"binding"`
	SensorID                string             `json:"sensor_id"`
	Source                  string             `json:"source"`
	CreatedAt               time.Time          `json:"created_at"`
	ExpiresAt               time.Time          `json:"expires_at"`
	RouteObservedAt         time.Time          `json:"route_observed_at"`
	RouteFreshUntil         time.Time          `json:"route_fresh_until"`
	OutsideEnrolledPrefixes bool               `json:"outside_enrolled_prefixes"`
	Policy                  HTTPSPlanPolicy    `json:"policy"`
	Budget                  HTTPSPlanBudget    `json:"budget"`
	RequestBytes            string             `json:"request_bytes"`
	Privacy                 []string           `json:"privacy"`
	Limitations             []string           `json:"limitations"`
}
type HTTPSPlanBudget struct {
	MaxConnections          int   `json:"max_connections"`
	MaxRequests             int   `json:"max_requests"`
	MaxRetries              int   `json:"max_retries"`
	MaxRequestBytes         int   `json:"max_request_bytes"`
	MaxResponseHeaderBytes  int   `json:"max_response_header_bytes"`
	MaxTransportReadBytes   int   `json:"max_transport_read_bytes"`
	MaxTransportWriteBytes  int   `json:"max_transport_write_bytes"`
	MaxTransportReadCalls   int   `json:"max_transport_read_calls"`
	MaxTransportWriteCalls  int   `json:"max_transport_write_calls"`
	ConnectTimeoutMS        int64 `json:"connect_timeout_ms"`
	TLSHandshakeTimeoutMS   int64 `json:"tls_handshake_timeout_ms"`
	ResponseHeaderTimeoutMS int64 `json:"response_header_timeout_ms"`
	TotalTimeoutMS          int64 `json:"total_timeout_ms"`
	MaxConcurrentRuns       int   `json:"max_concurrent_runs"`
	MinRunIntervalMS        int64 `json:"min_run_interval_ms"`
}
type HTTPSPlanPolicy struct {
	ALPN                 string `json:"alpn"`
	TrustStore           string `json:"trust_store"`
	ClientAuthentication bool   `json:"client_authentication"`
	HTTPVersion          string `json:"http_version"`
	MinTLSVersion        string `json:"min_tls_version"`
	MaxTLSVersion        string `json:"max_tls_version"`
	VerifyServerIdentity bool   `json:"verify_server_identity"`
	FreshConnection      bool   `json:"fresh_connection"`
	SessionResumption    bool   `json:"session_resumption"`
	EarlyData            bool   `json:"early_data"`
	UseProxy             bool   `json:"use_proxy"`
	ResolveNames         bool   `json:"resolve_names"`
	FollowRedirects      bool   `json:"follow_redirects"`
	ReadResponseBody     bool   `json:"read_response_body"`
}
