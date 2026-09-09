package api

import "time"

type CoverageDimension struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type CoverageScope struct {
	Configured         []CoverageDimension `json:"configured"`
	Verified           []CoverageDimension `json:"verified"`
	ExpectedUnverified []CoverageDimension `json:"expected_unverified"`
}

type CoverageSource struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Expected bool   `json:"expected"`
	Observed bool   `json:"observed"`
	NextStep string `json:"next_step,omitempty"`
}

type CoverageEvidenceWindow struct {
	HasEvidence bool       `json:"has_evidence"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	FreshUntil  *time.Time `json:"fresh_until,omitempty"`
}

type CoverageCadence struct {
	Mode       string `json:"mode"`
	IntervalMS int64  `json:"interval_ms,omitempty"`
	DwellMS    int64  `json:"dwell_ms,omitempty"`
}

type CoverageGap struct {
	ID         string              `json:"id"`
	Kind       string              `json:"kind"`
	Summary    string              `json:"summary"`
	Detail     string              `json:"detail"`
	NextStep   string              `json:"next_step"`
	Dimensions []CoverageDimension `json:"dimensions"`
	Directions []string            `json:"directions"`
}

type CoverageObservationPoint struct {
	ID         string                 `json:"id"`
	Kind       string                 `json:"kind"`
	SensorID   string                 `json:"sensor_id,omitempty"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason"`
	Scope      CoverageScope          `json:"scope"`
	Sources    []CoverageSource       `json:"sources"`
	Directions []string               `json:"directions"`
	Window     CoverageEvidenceWindow `json:"window"`
	Cadence    CoverageCadence        `json:"cadence"`
	Gaps       []CoverageGap          `json:"gaps"`
	NextStep   string                 `json:"next_step"`
}

type CoverageReport struct {
	CapabilityID      string                     `json:"capability_id"`
	Configured        bool                       `json:"configured"`
	State             string                     `json:"state"`
	Reason            string                     `json:"reason"`
	ObservationPoints []CoverageObservationPoint `json:"observation_points"`
	NextStep          string                     `json:"next_step"`
}
