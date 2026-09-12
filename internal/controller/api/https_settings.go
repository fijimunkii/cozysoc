package api

import "time"

const (
	MethodHTTPSSave   = "network-quality.https-save"
	MethodHTTPSList   = "network-quality.https-list"
	MethodHTTPSRetire = "network-quality.https-retire"
)

// Native authenticated configuration only. No source, interface, budget, ticket,
// scheduling, enablement or approval can be supplied through these methods.
type HTTPSSettingsParams struct {
	Endpoint          string `json:"endpoint"`
	ServerName        string `json:"server_name"`
	RequestTarget     string `json:"request_target"`
	Family            string `json:"family"`
	Method            string `json:"method"`
	ExpectedStatus    int    `json:"expected_status"`
	DestinationPolicy string `json:"destination_policy"`
}
type HTTPSIDParams struct {
	SelectionID string `json:"selection_id"`
}
type HTTPSSettings struct {
	SelectionID string              `json:"selection_id"`
	EndpointID  string              `json:"endpoint_id"`
	RequestID   string              `json:"request_id"`
	ScopeID     string              `json:"scope_id"`
	CreatedAt   time.Time           `json:"created_at"`
	Profile     string              `json:"profile"`
	Settings    HTTPSSettingsParams `json:"settings"`
}
type HTTPSSettingsResult struct {
	SchemaVersion  int             `json:"schema_version"`
	Mode           string          `json:"mode"`
	ConsentGranted bool            `json:"consent_granted"`
	Items          []HTTPSSettings `json:"items"`
}
type HTTPSRetireResult struct {
	SchemaVersion int    `json:"schema_version"`
	SelectionID   string `json:"selection_id"`
	State         string `json:"state"`
}
