package api

import (
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"time"
)

const MethodResolverCheck = "network-quality.resolver-check"

// The challenge correlates one decision on the authenticated review connection.
// It is not the server-held run ticket and cannot be reused on another connection.
type ResolverCheckReview struct {
	SchemaVersion           int                    `json:"schema_version"`
	Mode                    string                 `json:"mode"`
	Challenge               string                 `json:"challenge"`
	Profile                 string                 `json:"profile"`
	SelectionID             string                 `json:"selection_id"`
	ResolverID              string                 `json:"resolver_id"`
	QueryID                 string                 `json:"query_id"`
	Settings                ResolverSettingsParams `json:"settings"`
	Binding                 GatewayPlanBinding     `json:"binding"`
	SensorID                string                 `json:"sensor_id"`
	Source                  string                 `json:"source"`
	CreatedAt               time.Time              `json:"created_at"`
	ExpiresAt               time.Time              `json:"expires_at"`
	OutsideEnrolledPrefixes bool                   `json:"outside_enrolled_prefixes"`
	MayForwardUpstream      bool                   `json:"may_forward_upstream"`
	Budget                  ResolverPlanBudget     `json:"budget"`
}
type ResolverCheckDecision struct {
	Challenge string `json:"challenge"`
	Approve   *bool  `json:"approve"`
}
type ResolverCheckResult struct {
	SchemaVersion int                     `json:"schema_version"`
	Review        ResolverCheckReview     `json:"review"`
	RunID         string                  `json:"run_id,omitempty"`
	Outcome       string                  `json:"outcome"`
	FailureCode   string                  `json:"failure_code,omitempty"`
	Measurement   *ResolverRunMeasurement `json:"measurement,omitempty"`
}
type ResolverRunMeasurement struct {
	StartedAt               time.Time           `json:"started_at"`
	CompletedAt             time.Time           `json:"completed_at"`
	Exchange                nq.DNSExchangeState `json:"exchange"`
	Request                 nq.DNSRequestState  `json:"request"`
	Gap                     nq.GapReason        `json:"gap,omitempty"`
	Reply                   *nq.DNSReply        `json:"reply,omitempty"`
	ResponseTimeNanoseconds *int64              `json:"response_time_ns,omitempty"`
}
