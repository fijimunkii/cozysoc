package api

import "time"

// Router neighbor reports are historical source evidence. They are not
// confirmed device identities, presence, or network coverage.
type OPNsenseNeighborReport struct {
	ObservationID string    `json:"observation_id"`
	CapturedAt    time.Time `json:"captured_at"`
	Address       string    `json:"address"`
	Hardware      string    `json:"hardware_address"`
	Interface     string    `json:"interface"`
	Family        string    `json:"family"`
}

type OPNsenseNeighborHistory struct {
	ScopeEnrolled bool                     `json:"scope_enrolled"`
	ScopeID       string                   `json:"scope_id,omitempty"`
	AsOf          time.Time                `json:"as_of"`
	Reports       []OPNsenseNeighborReport `json:"reports"`
	Truncated     bool                     `json:"truncated"`
}
