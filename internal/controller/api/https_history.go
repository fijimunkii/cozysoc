package api

import "time"

const MethodHTTPSHistory = "network-quality.https-history"

// Omit params for the recent list; exact lookup accepts only a nonempty RunID.
// Neither form accepts scope, target, collection options, or consent.
type HTTPSHistoryParams struct {
	RunID string `json:"run_id"`
}

type HTTPSHistory struct {
	SchemaVersion int               `json:"schema_version"`
	Mode          string            `json:"mode"`
	Enrolled      bool              `json:"enrolled"`
	ScopeID       string            `json:"scope_id,omitempty"`
	LookupRunID   string            `json:"lookup_run_id,omitempty"`
	AsOf          time.Time         `json:"as_of"`
	Since         *time.Time        `json:"since,omitempty"`
	Limit         int               `json:"limit"`
	ScanLimit     int               `json:"scan_limit"`
	Truncated     bool              `json:"truncated"`
	ScanTruncated bool              `json:"scan_truncated"`
	Runs          []HTTPSHistoryRun `json:"runs"`
	Limitations   []string          `json:"limitations"`
}

// Every item is historical, including one recorded immediately before the read.
// Retained phase flags are not runtime state or renewed execution authority.
type HTTPSHistoryRun struct {
	RunID                 string                    `json:"run_id"`
	AuditSchemaVersion    int                       `json:"audit_schema_version"`
	Profile               string                    `json:"profile"`
	Selection             HTTPSHistorySelection     `json:"selection"`
	Observer              HTTPSHistoryObserver      `json:"observer"`
	LastAuditAt           time.Time                 `json:"last_audit_at"`
	AuthorizationRetained bool                      `json:"authorization_retained"`
	AdmissionRetained     bool                      `json:"admission_retained"`
	TerminalRetained      bool                      `json:"terminal_retained"`
	Outcome               string                    `json:"outcome"`
	Reason                string                    `json:"reason,omitempty"`
	Measurement           *HTTPSRunMeasurement      `json:"measurement,omitempty"`
	Assessment            HTTPSHistoricalAssessment `json:"assessment"`
}
type HTTPSHistorySelection struct {
	ID             string `json:"id"`
	EndpointID     string `json:"endpoint_id"`
	RequestID      string `json:"request_id"`
	Family         string `json:"family"`
	Method         string `json:"method"`
	ExpectedStatus int    `json:"expected_status"`
}
type HTTPSHistoryObserver struct {
	ScopeID        string `json:"scope_id"`
	SensorID       string `json:"sensor_id"`
	InterfaceName  string `json:"interface_name"`
	InterfaceIndex int    `json:"interface_index"`
}
type HTTPSHistoricalAssessment struct {
	State              string `json:"state"`
	Confidence         string `json:"confidence"`
	Summary            string `json:"summary"`
	NextStep           string `json:"next_step"`
	ExpectationMatched *bool  `json:"expectation_matched,omitempty"`
}
