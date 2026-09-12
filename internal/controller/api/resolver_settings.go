package api

import "time"

const (
	MethodResolverSave   = "network-quality.resolver-save"
	MethodResolverList   = "network-quality.resolver-list"
	MethodResolverRetire = "network-quality.resolver-retire"
	MethodResolverPlan   = "network-quality.resolver-plan"
)

// Native authenticated configuration only. No source, interface, budget, ticket,
// scheduling, enablement or approval can be supplied through these methods.
type ResolverSettingsParams struct {
	Endpoint         string `json:"endpoint"`
	Name             string `json:"name"`
	Family           string `json:"family"`
	Transport        string `json:"transport"`
	QueryType        string `json:"query_type"`
	Expect           string `json:"expect"`
	DestinationScope string `json:"destination_scope"`
}
type ResolverIDParams struct {
	SelectionID string `json:"selection_id"`
}
type ResolverSettings struct {
	SelectionID string                 `json:"selection_id"`
	ResolverID  string                 `json:"resolver_id"`
	QueryID     string                 `json:"query_id"`
	ScopeID     string                 `json:"scope_id"`
	CreatedAt   time.Time              `json:"created_at"`
	Settings    ResolverSettingsParams `json:"settings"`
}
type ResolverSettingsResult struct {
	SchemaVersion  int                `json:"schema_version"`
	Mode           string             `json:"mode"`
	ConsentGranted bool               `json:"consent_granted"`
	Items          []ResolverSettings `json:"items"`
}
type ResolverRetireResult struct {
	SchemaVersion int    `json:"schema_version"`
	SelectionID   string `json:"selection_id"`
	State         string `json:"state"`
}
type ResolverPlan struct {
	SchemaVersion           int                `json:"schema_version"`
	Mode                    string             `json:"mode"`
	ExecutionAvailable      bool               `json:"execution_available"`
	ConsentGranted          bool               `json:"consent_granted"`
	Profile                 string             `json:"profile"`
	Configuration           ResolverSettings   `json:"configuration"`
	Binding                 GatewayPlanBinding `json:"binding"`
	SensorID                string             `json:"sensor_id"`
	Source                  string             `json:"source"`
	CreatedAt               time.Time          `json:"created_at"`
	ExpiresAt               time.Time          `json:"expires_at"`
	OutsideEnrolledPrefixes bool               `json:"outside_enrolled_prefixes"`
	MayForwardUpstream      bool               `json:"may_forward_upstream"`
	Budget                  ResolverPlanBudget `json:"budget"`
	Limitations             []string           `json:"limitations"`
}
type ResolverPlanBudget struct {
	MaxSendCalls         int   `json:"max_send_calls"`
	MaxRequestBytes      int   `json:"max_request_bytes"`
	MaxReplyBytes        int   `json:"max_reply_bytes"`
	MaxReceivedDatagrams int   `json:"max_received_datagrams"`
	MaxReceiveCalls      int   `json:"max_receive_calls"`
	ExchangeTimeoutMS    int64 `json:"exchange_timeout_ms"`
	TotalTimeoutMS       int64 `json:"total_timeout_ms"`
	MaxConcurrentRuns    int   `json:"max_concurrent_runs"`
	MinRunIntervalMS     int64 `json:"min_run_interval_ms"`
}
