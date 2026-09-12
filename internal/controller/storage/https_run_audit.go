package storage

import (
	"context"
	"encoding/json"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
)

const HTTPSRunAuditKind = "https-run"

var _ httpsrun.Auditor = (*Store)(nil)

// InsertHTTPSRunAudit uses the existing durable, quota/retention-controlled
// audit store. It does not persist tickets or grant/recover execution authority.
// One unique row per run/phase avoids silently duplicating lifecycle evidence.
func (s *Store) InsertHTTPSRunAudit(ctx context.Context, event httpsrun.Event) error {
	if s == nil || s.conn == nil {
		return httpsrun.ErrAudit
	}
	if err := httpsrun.ValidateEvent(event); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return httpsrun.ErrAudit
	}
	actor := "controller"
	if event.State == "authorized" {
		actor = "local-os-user"
	}
	return s.InsertAuditEvent(ctx, domain.AuditEvent{
		ID:   "audit.https-run." + event.RunID + "." + event.State,
		Kind: HTTPSRunAuditKind, Actor: actor, OccurredAt: event.At.UTC(), SchemaVersion: event.SchemaVersion,
		Payload: payload, Retention: domain.RetentionAudit,
	})
}
