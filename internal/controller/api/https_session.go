package api

import (
	"time"

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

const MethodHTTPSCheck = "network-quality.https-check"

// The challenge correlates one decision on the authenticated review connection.
// It is not the server-held run ticket and cannot be reused on another connection.
type HTTPSCheckReview struct {
	SchemaVersion           int                 `json:"schema_version"`
	Mode                    string              `json:"mode"`
	Challenge               string              `json:"challenge"`
	Profile                 string              `json:"profile"`
	SelectionID             string              `json:"selection_id"`
	EndpointID              string              `json:"endpoint_id"`
	RequestID               string              `json:"request_id"`
	Settings                HTTPSSettingsParams `json:"settings"`
	Binding                 GatewayPlanBinding  `json:"binding"`
	SensorID                string              `json:"sensor_id"`
	Source                  string              `json:"source"`
	CreatedAt               time.Time           `json:"created_at"`
	ExpiresAt               time.Time           `json:"expires_at"`
	OutsideEnrolledPrefixes bool                `json:"outside_enrolled_prefixes"`
	RouteObservedAt         time.Time           `json:"route_observed_at"`
	RouteFreshUntil         time.Time           `json:"route_fresh_until"`
	Policy                  HTTPSPlanPolicy     `json:"policy"`
	RequestBytes            string              `json:"request_bytes"`
	Privacy                 []string            `json:"privacy"`
	Budget                  HTTPSPlanBudget     `json:"budget"`
}
type HTTPSCheckDecision struct {
	Challenge string `json:"challenge"`
	Approve   *bool  `json:"approve"`
}
type HTTPSCheckResult struct {
	SchemaVersion int                  `json:"schema_version"`
	Review        HTTPSCheckReview     `json:"review"`
	RunID         string               `json:"run_id,omitempty"`
	Outcome       string               `json:"outcome"`
	FailureCode   string               `json:"failure_code,omitempty"`
	Measurement   *HTTPSRunMeasurement `json:"measurement,omitempty"`
}
type HTTPSRunMeasurement struct {
	StartedAt               time.Time            `json:"started_at"`
	CompletedAt             time.Time            `json:"completed_at"`
	Exchange                nq.HTTPSExchange     `json:"exchange"`
	Request                 nq.HTTPSRequestState `json:"request"`
	Gap                     nq.GapReason         `json:"gap,omitempty"`
	Stage                   nq.HTTPSStage        `json:"stage"`
	StatusCode              int                  `json:"status_code,omitempty"`
	ResponseTimeNanoseconds *int64               `json:"response_time_ns,omitempty"`
}
