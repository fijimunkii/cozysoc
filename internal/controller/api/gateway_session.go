package api

import "time"

// This is a dedicated two-frame native session, not a generic RPC or a browser
// endpoint. The decision is accepted only on the authenticated review connection.
const MethodGatewayCheck = "network-quality.gateway-check"

// GatewayCheckReview describes one experimental check. Challenge correlates a
// decision on this connection only; it is not the process-local approval ticket.
type GatewayCheckReview struct {
	SchemaVersion       int                `json:"schema_version"`
	Mode                string             `json:"mode"`
	GatewayRoleVerified bool               `json:"gateway_role_verified"`
	Challenge           string             `json:"challenge"`
	Profile             string             `json:"profile"`
	CreatedAt           time.Time          `json:"created_at"`
	ExpiresAt           time.Time          `json:"expires_at"`
	Binding             GatewayPlanBinding `json:"binding"`
	Target              string             `json:"target"`
	Source              string             `json:"source"`
	Budget              GatewayPlanBudget  `json:"budget"`
}

// GatewayCheckDecision has no target, interface, source, budget, or ticket field.
// Omitted/null approval is invalid, never implicit approval or implicit decline.
type GatewayCheckDecision struct {
	Challenge string `json:"challenge"`
	Approve   *bool  `json:"approve"`
}

// GatewayCheckResult separates the completed protocol exchange from execution
// and measurement. Even an error-free exchange may report a failed/canceled run.
// Review is historical context, not a claim of continued enrollment or freshness.
type GatewayCheckResult struct {
	SchemaVersion int                    `json:"schema_version"`
	Review        GatewayCheckReview     `json:"review"`
	RunID         string                 `json:"run_id,omitempty"`
	Outcome       string                 `json:"outcome"`
	FailureCode   string                 `json:"failure_code,omitempty"`
	Measurement   *GatewayRunMeasurement `json:"measurement,omitempty"`
}

type GatewayRunMeasurement struct {
	StartedAt          time.Time  `json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	SendCalls          int        `json:"send_calls"`
	AcceptedRequests   int        `json:"accepted_requests"`
	Replies            int        `json:"replies"`
	Timeouts           int        `json:"timeouts"`
	Complete           bool       `json:"complete"`
	MeanRTTNanoseconds *int64     `json:"mean_rtt_ns,omitempty"`
}
