package api

import (
	"encoding/json"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

const Version = 1

const (
	MethodStatus              = "status"
	MethodHealth              = "health"
	MethodCapabilitiesList    = "capabilities.list"
	MethodDevicesList         = "devices.list"
	MethodDeviceLabel         = "device.label"
	MethodNetworksList        = "networks.list"
	MethodNetworkEnroll       = "network.enroll"
	MethodDeviceWatchCoverage = "device-watch.coverage"
	MethodDeviceWatchEnable   = "device-watch.enable"
	MethodDeviceWatchDisable  = "device-watch.disable"
)

type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Auth    string          `json:"auth"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Status struct {
	APIVersion          int       `json:"api_version"`
	ControllerVersion   string    `json:"controller_version"`
	PID                 int       `json:"pid"`
	StartedAt           time.Time `json:"started_at"`
	UptimeMS            int64     `json:"uptime_ms"`
	ConfigSchemaVersion int       `json:"config_schema_version"`
	Transport           string    `json:"transport"`
}

type Health struct {
	State      string     `json:"state"`
	LastTickAt time.Time  `json:"last_tick_at"`
	GapCount   uint64     `json:"gap_count"`
	LastGapAt  *time.Time `json:"last_gap_at,omitempty"`
}

type CapabilityList struct {
	CatalogSchemaVersion int                   `json:"catalog_schema_version"`
	Capabilities         []capability.Instance `json:"capabilities"`
}

type DevicePresence struct {
	ID        string    `json:"id"`
	UserLabel string    `json:"user_label,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	State     string    `json:"state"`
}

type DeviceList struct {
	Configured bool             `json:"configured"`
	ScopeID    string           `json:"scope_id,omitempty"`
	AsOf       time.Time        `json:"as_of"`
	Devices    []DevicePresence `json:"devices"`
	Truncated  bool             `json:"truncated"`
}

type DeviceLabelParams struct {
	DeviceID string  `json:"device_id"`
	Label    *string `json:"label"`
}

type DeviceLabelResult struct {
	DeviceID  string `json:"device_id"`
	UserLabel string `json:"user_label,omitempty"`
	Changed   bool   `json:"changed"`
}

type NetworkInterface struct {
	InterfaceName  string   `json:"interface_name"`
	InterfaceIndex int      `json:"interface_index"`
	Prefixes       []string `json:"prefixes"`
}

type EnrolledNetwork struct {
	ScopeID    string           `json:"scope_id"`
	EnrolledAt time.Time        `json:"enrolled_at"`
	Interface  NetworkInterface `json:"interface"`
}

type NetworkList struct {
	Candidates          []NetworkInterface `json:"candidates"`
	CandidatesTruncated bool               `json:"candidates_truncated"`
	Enrolled            *EnrolledNetwork   `json:"enrolled,omitempty"`
}

type NetworkEnrollParams struct {
	InterfaceName string `json:"interface_name"`
}

type NetworkEnrollResult struct {
	ScopeID    string           `json:"scope_id"`
	EnrolledAt time.Time        `json:"enrolled_at"`
	Interface  NetworkInterface `json:"interface"`
	Changed    bool             `json:"changed"`
}

type DeviceWatchCoverageSource struct {
	ID                    string `json:"id"`
	AddressFamily         string `json:"address_family"`
	State                 string `json:"state"`
	Reported              bool   `json:"reported"`
	AvailableAtLastSample bool   `json:"available_at_last_sample"`
	NextStep              string `json:"next_step,omitempty"`
}

type DeviceWatchCoverageBlindSpot struct {
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Detail   string `json:"detail"`
	NextStep string `json:"next_step"`
}

type DeviceWatchSensorHealth struct {
	State            string     `json:"state"`
	Running          bool       `json:"running"`
	LastAttemptAt    *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessfulAt *time.Time `json:"last_successful_at,omitempty"`
	LastErrorClass   string     `json:"last_error_class,omitempty"`
	NextStep         string     `json:"next_step,omitempty"`
}

type DeviceWatchPipelineHealth struct {
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Capacity int    `json:"capacity"`
	Depth    int    `json:"depth"`
	Dropped  uint64 `json:"dropped_total"`
	Failed   uint64 `json:"failed_total"`
	NextStep string `json:"next_step,omitempty"`
}

type DeviceWatchDatabaseHealth struct {
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
	DatabaseBytes  int64  `json:"database_bytes"`
	UsedBytes      int64  `json:"used_bytes"`
	ReusableBytes  int64  `json:"reusable_bytes"`
	MaxBytes       int64  `json:"max_bytes"`
	NextStep       string `json:"next_step,omitempty"`
}

type DeviceWatchOperationalHealth struct {
	Sensor   DeviceWatchSensorHealth   `json:"sensor"`
	Pipeline DeviceWatchPipelineHealth `json:"pipeline"`
	Database DeviceWatchDatabaseHealth `json:"database"`
}

type DeviceWatchCoverage struct {
	Configured       bool                           `json:"configured"`
	ScopeID          string                         `json:"scope_id,omitempty"`
	AsOf             time.Time                      `json:"as_of"`
	State            string                         `json:"state"`
	Reason           string                         `json:"reason,omitempty"`
	SensorID         string                         `json:"sensor_id,omitempty"`
	InterfaceName    string                         `json:"interface_name,omitempty"`
	EvidenceAt       *time.Time                     `json:"evidence_at,omitempty"`
	FreshUntil       *time.Time                     `json:"fresh_until,omitempty"`
	NeighborsInScope *int                           `json:"neighbors_in_scope,omitempty"`
	Sources          []DeviceWatchCoverageSource    `json:"sources"`
	Operational      *DeviceWatchOperationalHealth  `json:"operational,omitempty"`
	BlindSpots       []DeviceWatchCoverageBlindSpot `json:"blind_spots"`
	NextStep         string                         `json:"next_step"`
}

type DeviceWatchControlResult struct {
	ScopeID string                   `json:"scope_id,omitempty"`
	Changed bool                     `json:"changed"`
	Active  bool                     `json:"active"`
	State   capability.InstanceState `json:"state"`
}
