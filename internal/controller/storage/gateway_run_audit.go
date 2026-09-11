package storage

import (
	"context"
	"encoding/json"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
)

const GatewayRunAuditKind = "gateway-run"

var _ gatewayrun.Auditor = (*Store)(nil)

// InsertGatewayRunAudit uses the existing durable, quota/retention-controlled
// audit store. It does not persist tickets or grant/recover execution authority.
// One unique row per run/phase avoids silently duplicating lifecycle evidence.
func (s *Store) InsertGatewayRunAudit(ctx context.Context, event gatewayrun.Event) error {
	if s == nil || s.conn == nil {
		return gatewayrun.ErrAudit
	}
	if err := gatewayrun.ValidateEvent(event); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return gatewayrun.ErrAudit
	}
	actor := "controller"
	if event.State == "authorized" {
		actor = "local-os-user"
	}
	return s.InsertAuditEvent(ctx, domain.AuditEvent{
		ID:   "audit.gateway-run." + event.RunID + "." + event.State,
		Kind: GatewayRunAuditKind, Actor: actor, OccurredAt: event.At.UTC(), SchemaVersion: 1,
		Payload: payload, Retention: domain.RetentionAudit,
	})
}
