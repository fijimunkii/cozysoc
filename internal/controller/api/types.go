package api

import (
	"encoding/json"
	"time"
)

const Version = 1

const (
	MethodStatus = "status"
	MethodHealth = "health"
)

type Request struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Method  string `json:"method"`
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
