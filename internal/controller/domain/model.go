package domain

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

const MaxJSONBytes = 64 * 1024

type RetentionClass string

const (
	RetentionEphemeral RetentionClass = "ephemeral"
	RetentionShort     RetentionClass = "short"
	RetentionStandard  RetentionClass = "standard"
	RetentionAudit     RetentionClass = "audit"
)

type ClaimKind string

const (
	ClaimIPv4         ClaimKind = "ipv4"
	ClaimIPv6         ClaimKind = "ipv6"
	ClaimMAC          ClaimKind = "mac"
	ClaimHostname     ClaimKind = "hostname"
	ClaimServiceName  ClaimKind = "service-name"
	ClaimDHCPClientID ClaimKind = "dhcp-client-id"
	ClaimEndpointID   ClaimKind = "endpoint-id"
)

type LinkAuthority string

const (
	LinkInferred LinkAuthority = "inferred"
	LinkUser     LinkAuthority = "user"
)

type NetworkScope struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	EnrolledAt time.Time       `json:"enrolled_at"`
	RetiredAt  *time.Time      `json:"retired_at,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
}

type Sensor struct {
	ID           string          `json:"id"`
	ScopeID      string          `json:"scope_id"`
	Kind         string          `json:"kind"`
	Ownership    string          `json:"ownership"`
	RegisteredAt time.Time       `json:"registered_at"`
	Metadata     json.RawMessage `json:"metadata"`
}

type Observation struct {
	ID            string          `json:"id"`
	ScopeID       string          `json:"scope_id"`
	SensorID      string          `json:"sensor_id"`
	Kind          string          `json:"kind"`
	SourceStream  string          `json:"source_stream"`
	SourceKey     string          `json:"source_key"`
	SourceEventID string          `json:"source_event_id,omitempty"`
	SourceTime    *time.Time      `json:"source_time,omitempty"`
	IngestedAt    time.Time       `json:"ingested_at"`
	SchemaVersion int             `json:"schema_version"`
	Confidence    *float64        `json:"confidence,omitempty"`
	Attribution   string          `json:"attribution"`
	Payload       json.RawMessage `json:"payload"`
	Retention     RetentionClass  `json:"retention"`
}

type Device struct {
	ID        string     `json:"id"`
	UserLabel string     `json:"user_label,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

type IdentityClaim struct {
	ID                  string         `json:"id"`
	ScopeID             string         `json:"scope_id"`
	Kind                ClaimKind      `json:"kind"`
	Value               string         `json:"value"`
	ObservedAt          time.Time      `json:"observed_at"`
	ValidUntil          *time.Time     `json:"valid_until,omitempty"`
	Confidence          *float64       `json:"confidence,omitempty"`
	SourceSensorID      string         `json:"source_sensor_id"`
	SourceObservationID string         `json:"source_observation_id,omitempty"`
	Retention           RetentionClass `json:"retention"`
}

type DeviceClaimLink struct {
	ID                    string        `json:"id"`
	DeviceID              string        `json:"device_id"`
	ClaimID               string        `json:"claim_id"`
	ValidFrom             time.Time     `json:"valid_from"`
	ValidUntil            *time.Time    `json:"valid_until,omitempty"`
	Confidence            *float64      `json:"confidence,omitempty"`
	Authority             LinkAuthority `json:"authority"`
	Reason                string        `json:"reason"`
	EvidenceObservationID string        `json:"evidence_observation_id,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
}

type CoverageSample struct {
	ID            string          `json:"id"`
	ScopeID       string          `json:"scope_id"`
	SensorID      string          `json:"sensor_id"`
	CapabilityID  string          `json:"capability_id"`
	Status        string          `json:"status"`
	StartedAt     time.Time       `json:"started_at"`
	EndedAt       time.Time       `json:"ended_at"`
	SchemaVersion int             `json:"schema_version"`
	Evidence      json.RawMessage `json:"evidence"`
	Retention     RetentionClass  `json:"retention"`
}

type Finding struct {
	ID                     string          `json:"id"`
	ScopeID                string          `json:"scope_id"`
	DetectorID             string          `json:"detector_id"`
	DetectorVersion        string          `json:"detector_version"`
	Category               string          `json:"category"`
	Severity               string          `json:"severity"`
	Confidence             *float64        `json:"confidence,omitempty"`
	ObservedAt             time.Time       `json:"observed_at"`
	CreatedAt              time.Time       `json:"created_at"`
	SchemaVersion          int             `json:"schema_version"`
	Payload                json.RawMessage `json:"payload"`
	EvidenceObservationIDs []string        `json:"evidence_observation_ids"`
	Retention              RetentionClass  `json:"retention"`
}

type AuditEvent struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Actor         string          `json:"actor"`
	OccurredAt    time.Time       `json:"occurred_at"`
	SchemaVersion int             `json:"schema_version"`
	Payload       json.RawMessage `json:"payload"`
	Retention     RetentionClass  `json:"retention"`
}

type IngestionCheckpoint struct {
	SensorID  string    `json:"sensor_id"`
	StreamID  string    `json:"stream_id"`
	Cursor    string    `json:"cursor"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Activity is a query projection over immutable observations and device links.
// It is intentionally not a second persisted event stream.
type Activity struct {
	ObservationID string    `json:"observation_id"`
	DeviceIDs     []string  `json:"device_ids"`
	Kind          string    `json:"kind"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type StorageEvent struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	OccurredAt time.Time       `json:"occurred_at"`
	Details    json.RawMessage `json:"details"`
}
