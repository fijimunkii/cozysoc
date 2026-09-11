package api

import "time"

const MethodGatewayHistory = "network-quality.gateway-history"

// Omit params for the recent list; exact lookup accepts only a nonempty RunID.
// Neither form accepts scope, target, collection options, or consent.
type GatewayHistoryParams struct {
	RunID string `json:"run_id"`
}

type GatewayHistory struct {
	SchemaVersion int                 `json:"schema_version"`
	Mode          string              `json:"mode"`
	Enrolled      bool                `json:"enrolled"`
	ScopeID       string              `json:"scope_id,omitempty"`
	LookupRunID   string              `json:"lookup_run_id,omitempty"`
	AsOf          time.Time           `json:"as_of"`
	Since         *time.Time          `json:"since,omitempty"`
	Limit         int                 `json:"limit"`
	ScanLimit     int                 `json:"scan_limit"`
	Truncated     bool                `json:"truncated"`
	ScanTruncated bool                `json:"scan_truncated"`
	Runs          []GatewayHistoryRun `json:"runs"`
	Limitations   []string            `json:"limitations"`
}

// Every item is historical, including one recorded immediately before the read.
// Retained phase flags are not runtime state or renewed execution authority.
type GatewayHistoryRun struct {
	RunID                 string                      `json:"run_id"`
	AuditSchemaVersion    int                         `json:"audit_schema_version"`
	Profile               string                      `json:"profile"`
	InterfaceName         string                      `json:"interface_name"`
	InterfaceIndex        int                         `json:"interface_index"`
	Target                string                      `json:"target"`
	Source                string                      `json:"source"`
	GatewayRoleVerified   bool                        `json:"gateway_role_verified"`
	LastAuditAt           time.Time                   `json:"last_audit_at"`
	AuthorizationRetained bool                        `json:"authorization_retained"`
	AdmissionRetained     bool                        `json:"admission_retained"`
	TerminalRetained      bool                        `json:"terminal_retained"`
	Outcome               string                      `json:"outcome"`
	Reason                string                      `json:"reason,omitempty"`
	Measurement           *GatewayRunMeasurement      `json:"measurement,omitempty"`
	Assessment            GatewayHistoricalAssessment `json:"assessment"`
}

type GatewayHistoricalAssessment struct {
	State            string   `json:"state"`
	Confidence       string   `json:"confidence"`
	Summary          string   `json:"summary"`
	NextStep         string   `json:"next_step"`
	ReplyLossPercent *float64 `json:"reply_loss_percent,omitempty"`
}
