package api

import "time"

// DiagnosticPreview is a deliberately small support snapshot. It contains no
// configuration, network identity, evidence payload, path, URL, or raw error.
type DiagnosticPreview struct {
	SchemaVersion int                  `json:"schema_version"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Controller    DiagnosticController `json:"controller"`
	Modules       []DiagnosticModule   `json:"modules"`
	Coverage      []DiagnosticCoverage `json:"coverage"`
	Storage       DiagnosticStorage    `json:"storage"`
}

type DiagnosticController struct {
	BuildVersion        string `json:"build_version"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
	HealthState         string `json:"health_state"`
	GapCount            uint64 `json:"gap_count"`
}

type DiagnosticModule struct {
	ID           string `json:"id"`
	BuildVersion string `json:"build_version"`
	Desired      string `json:"desired"`
	Verification string `json:"verification"`
}

type DiagnosticCoverage struct {
	CapabilityID    string `json:"capability_id"`
	State           string `json:"state"`
	FailureCategory string `json:"failure_category"`
}

// DiagnosticStorage keeps quota and host-volume health independent without
// exposing database size, free space, filesystem paths, or raw read errors.
type DiagnosticStorage struct {
	ReadState   string `json:"read_state"`
	QuotaState  string `json:"quota_state"`
	VolumeState string `json:"volume_state"`
}
