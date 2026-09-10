package devicewatch

import (
	"context"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const presenceFreshWindow = 3 * time.Minute

type PresenceState string

const (
	PresenceVisible   PresenceState = "visible"
	PresenceUncertain PresenceState = "uncertain"
)

type DevicePresence struct {
	ID        string        `json:"id"`
	UserLabel string        `json:"user_label,omitempty"`
	FirstSeen time.Time     `json:"first_seen"`
	LastSeen  time.Time     `json:"last_seen"`
	State     PresenceState `json:"state"`
}

type PresencePage struct {
	ScopeID string           `json:"scope_id"`
	AsOf    time.Time        `json:"as_of"`
	Devices []DevicePresence `json:"devices"`
	NextID  string           `json:"next_id,omitempty"`
}

type DeviceEvidenceReader interface {
	ListDeviceEvidence(context.Context, storage.DeviceEvidenceQuery) (storage.DeviceEvidencePage, error)
}

func ListPresence(ctx context.Context, reader DeviceEvidenceReader, scopeID string, asOf time.Time, afterID string, limit int) (PresencePage, error) {
	if reader == nil {
		return PresencePage{}, fmt.Errorf("device presence reader is required")
	}
	if asOf.IsZero() {
		asOf = time.Now().UTC()
	} else {
		asOf = asOf.UTC()
	}
	evidence, err := reader.ListDeviceEvidence(ctx, storage.DeviceEvidenceQuery{
		ScopeID: scopeID,
		AsOf:    asOf,
		AfterID: afterID,
		Limit:   limit,
	})
	if err != nil {
		return PresencePage{}, err
	}
	page := PresencePage{
		ScopeID: scopeID,
		AsOf:    asOf,
		Devices: make([]DevicePresence, 0, len(evidence.Devices)),
		NextID:  evidence.NextID,
	}
	for _, summary := range evidence.Devices {
		page.Devices = append(page.Devices, PresenceFromEvidence(summary, asOf))
	}
	return page, nil
}

func PresenceFromEvidence(summary storage.DeviceEvidenceSummary, asOf time.Time) DevicePresence {
	asOf = asOf.UTC()
	state := PresenceUncertain
	age := asOf.Sub(summary.LastSeen)
	if age >= 0 && age <= presenceFreshWindow {
		state = PresenceVisible
	}
	return DevicePresence{
		ID:        summary.Device.ID,
		UserLabel: summary.Device.UserLabel,
		FirstSeen: summary.FirstSeen,
		LastSeen:  summary.LastSeen,
		State:     state,
	}
}
