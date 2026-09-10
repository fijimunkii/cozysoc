package api

import "time"

const MethodNetworkQualityLocal = "network-quality.local"

// LocalNetworkQuality is an on-demand read of OS interface metadata, not a
// configured probe capability, stored history, or a network-wide verdict.
// Observer and Check are absent when there is no enrolled network.
type LocalNetworkQuality struct {
	Enrolled    bool                    `json:"enrolled"`
	AsOf        time.Time               `json:"as_of"`
	Observer    *NetworkQualityObserver `json:"observer,omitempty"`
	Check       *LocalInterfaceCheck    `json:"check,omitempty"`
	Limitations []string                `json:"limitations"`
}

type NetworkQualityObserver struct {
	ScopeID        string `json:"scope_id"`
	SensorID       string `json:"sensor_id"`
	InterfaceName  string `json:"interface_name"`
	InterfaceIndex int    `json:"interface_index"`
}

// LocalInterfaceCheck intentionally has no latency, loss, speed, signal,
// physical-carrier, gateway, DNS, external-target, or raw OS fields.
// AdministrativeUp is absent when binding or collection could not be verified.
// EvidenceID identifies this transient sample, not a retrievable stored event.
type LocalInterfaceCheck struct {
	Source           string    `json:"source"`
	Layer            string    `json:"layer"`
	Method           string    `json:"method"`
	State            string    `json:"state"`
	Confidence       string    `json:"confidence"`
	EvidenceID       string    `json:"evidence_id"`
	StartedAt        time.Time `json:"started_at"`
	CompletedAt      time.Time `json:"completed_at"`
	FreshUntil       time.Time `json:"fresh_until"`
	Gap              string    `json:"gap,omitempty"`
	AdministrativeUp *bool     `json:"administrative_up,omitempty"`
	Summary          string    `json:"summary"`
	NextStep         string    `json:"next_step"`
}
