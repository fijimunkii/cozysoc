package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const CapabilityIntentAuditKind = "capability-intent"

func (s *Store) InsertCapabilityIntentAudit(ctx context.Context, capabilityID string, desired capability.DesiredState, state, scopeID, reason string) error {
	if s == nil || s.conn == nil {
		return fmt.Errorf("storage is unavailable")
	}
	if capabilityID == "" || len(capabilityID) > 128 || strings.TrimSpace(capabilityID) != capabilityID {
		return fmt.Errorf("invalid capability audit id")
	}
	if desired != capability.DesiredEnabled && desired != capability.DesiredDisabled {
		return fmt.Errorf("invalid capability audit desired state")
	}
	switch state {
	case "requested", "applied", "failed":
	default:
		return fmt.Errorf("invalid capability audit state")
	}
	if len(reason) > 128 || strings.TrimSpace(reason) != reason {
		return fmt.Errorf("invalid capability audit reason")
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid capability audit reason")
		}
	}

	payload := map[string]any{
		"schema_version": 1,
		"state":          state,
		"capability_id":  capabilityID,
		"desired":        desired,
	}
	if scopeID != "" {
		payload["scope_id"] = scopeID
	}
	if reason != "" {
		payload["reason"] = reason
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode capability intent audit: %w", err)
	}
	id, err := randomID("audit.capability-intent")
	if err != nil {
		return err
	}
	return s.InsertAuditEvent(ctx, domain.AuditEvent{
		ID:            id,
		Kind:          CapabilityIntentAuditKind,
		Actor:         "local-os-user",
		OccurredAt:    s.now().UTC(),
		SchemaVersion: 1,
		Payload:       encoded,
		Retention:     domain.RetentionAudit,
	})
}