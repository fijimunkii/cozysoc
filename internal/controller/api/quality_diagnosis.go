package api

import "time"

const MethodQualityDiagnosis = "network-quality.diagnosis"

// QualityDiagnosis is a comparison of retained runs, never current connectivity.
// It accepts no client-selected scope, time, target, policy or execution options.
type QualityDiagnosis struct {
	SchemaVersion       int                         `json:"schema_version"`
	Mode                string                      `json:"mode"`
	Enrolled            bool                        `json:"enrolled"`
	ScopeID             string                      `json:"scope_id,omitempty"`
	ReadAt              time.Time                   `json:"read_at"`
	Since               time.Time                   `json:"since"`
	RunLimitPerLayer    int                         `json:"run_limit_per_layer"`
	ScanLimitPerLayer   int                         `json:"scan_limit_per_layer"`
	Truncated           bool                        `json:"truncated"`
	ScanTruncated       bool                        `json:"scan_truncated"`
	AssessmentAt        *time.Time                  `json:"assessment_at,omitempty"`
	MaxCompletionSkewMS int                         `json:"max_completion_skew_ms"`
	FreshnessMS         int                         `json:"freshness_ms"`
	Selected            []QualityDiagnosisRun       `json:"selected"`
	Compared            []QualityDiagnosisReference `json:"compared"`
	EvidenceStart       *time.Time                  `json:"evidence_start,omitempty"`
	EvidenceEnd         *time.Time                  `json:"evidence_end,omitempty"`
	Conclusion          string                      `json:"conclusion"`
	Confidence          string                      `json:"confidence"`
	Summary             string                      `json:"summary"`
	NextStep            string                      `json:"next_step"`
	Limitations         []string                    `json:"limitations"`
}
type QualityDiagnosisReference struct {
	Kind  string `json:"kind"`
	RunID string `json:"run_id"`
}
type QualityDiagnosisRun struct {
	QualityDiagnosisReference
	InterfaceName    string                    `json:"interface_name"`
	InterfaceIndex   int                       `json:"interface_index"`
	LastAuditAt      time.Time                 `json:"last_audit_at"`
	ExecutionOutcome string                    `json:"execution_outcome"`
	SampleStatus     string                    `json:"sample_status"`
	StartedAt        *time.Time                `json:"started_at,omitempty"`
	CompletedAt      *time.Time                `json:"completed_at,omitempty"`
	Selection        *ResolverHistorySelection `json:"selection,omitempty"`
}
