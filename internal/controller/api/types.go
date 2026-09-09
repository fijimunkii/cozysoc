package api

import (
	"encoding/json"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
)

const Version = 1

const (
	MethodStatus           = "status"
	MethodHealth           = "health"
	MethodCapabilitiesList = "capabilities.list"
	MethodDevicesList      = "devices.list"
)

type Request struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Auth    string `json:"auth"`
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
