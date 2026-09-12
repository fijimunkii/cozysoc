package storage

import (
	"context"
	"encoding/json"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

const ResolverRunAuditKind = "resolver-run"

var _ resolverrun.Auditor = (*Store)(nil)

// InsertResolverRunAudit uses the existing durable, quota/retention-controlled
// audit store. It does not persist tickets or grant/recover execution authority.
// One unique row per run/phase avoids silently duplicating lifecycle evidence.
func (s *Store) InsertResolverRunAudit(ctx context.Context, event resolverrun.Event) error {
	if s == nil || s.conn == nil {
		return resolverrun.ErrAudit
	}
	if err := resolverrun.ValidateEvent(event); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return resolverrun.ErrAudit
	}
	actor := "controller"
	if event.State == "authorized" {
		actor = "local-os-user"
	}
	return s.InsertAuditEvent(ctx, domain.AuditEvent{
		ID:   "audit.resolver-run." + event.RunID + "." + event.State,
		Kind: ResolverRunAuditKind, Actor: actor, OccurredAt: event.At.UTC(), SchemaVersion: event.SchemaVersion,
		Payload: payload, Retention: domain.RetentionAudit,
	})
}
