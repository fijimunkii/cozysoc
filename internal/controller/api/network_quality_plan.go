package api

import "time"

const MethodNetworkQualityGatewayPlan = "network-quality.gateway-plan"

type GatewayPlanParams struct {
	Target string `json:"target"`
}

// GatewayCheckPlan is a native review-only DTO, never an execution credential.
// Budget fields are proposed ceilings; the executor is deliberately unavailable.
type GatewayCheckPlan struct {
	SchemaVersion      int                `json:"schema_version"`
	Mode               string             `json:"mode"`
	ExecutionAvailable bool               `json:"execution_available"`
	ConsentGranted     bool               `json:"consent_granted"`
	CreatedAt          time.Time          `json:"created_at"`
	ReviewExpiresAt    time.Time          `json:"review_expires_at"`
	Binding            GatewayPlanBinding `json:"binding"`
	Target             GatewayPlanTarget  `json:"target"`
	Method             string             `json:"method"`
	Route              GatewayRouteReview `json:"route"`
	ProposedBudget     GatewayPlanBudget  `json:"proposed_budget"`
	Limitations        []string           `json:"limitations"`
}

type GatewayPlanBinding struct {
	ScopeID        string   `json:"scope_id"`
	InterfaceName  string   `json:"interface_name"`
	InterfaceIndex int      `json:"interface_index"`
	Prefixes       []string `json:"prefixes"`
}

type GatewayPlanTarget struct {
	Address      string `json:"address"`
	Family       string `json:"family"`
	Role         string `json:"role"`
	RoleVerified bool   `json:"role_verified"`
}

type GatewayPlanBudget struct {
	MaxAttempts         int   `json:"max_attempts"`
	MinIntervalMS       int64 `json:"min_interval_ms"`
	AttemptTimeoutMS    int64 `json:"attempt_timeout_ms"`
	TotalTimeoutMS      int64 `json:"total_timeout_ms"`
	PayloadBytes        int   `json:"payload_bytes"`
	MaxICMPRequestBytes int   `json:"max_icmp_request_bytes"`
	MaxConcurrentRuns   int   `json:"max_concurrent_runs"`
	MinRunIntervalMS    int64 `json:"min_run_interval_ms"`
}
