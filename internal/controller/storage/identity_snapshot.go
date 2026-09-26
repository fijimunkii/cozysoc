package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type identityQueryReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// LegacyIdentitySnapshot reads legacy identity evidence through a caller-owned
// transaction. It is the legacy half of a future mixed-history reader: it must
// not be used alone once batch-only claims are persisted.
//
// The owner must use a dedicated connection, not Store.conn, and retain the same
// transaction through planning and commit. This reader never begins, commits or
// rolls back transactions. It must not escape into a long-lived runtime or queue.
type LegacyIdentitySnapshot struct {
	tx  *sql.Tx
	now time.Time
}

// NewLegacyIdentitySnapshot fixes the retention evaluation clock for all queries
// in the transaction. Evidence observation time remains a separate query window.
func NewLegacyIdentitySnapshot(tx *sql.Tx, now time.Time) (*LegacyIdentitySnapshot, error) {
	if tx == nil || now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return nil, fmt.Errorf("identity snapshot requires a transaction and valid evaluation time")
	}
	return &LegacyIdentitySnapshot{tx: tx, now: now.UTC()}, nil
}

func (r *LegacyIdentitySnapshot) FindRecentDevicesByClaim(ctx context.Context, scopeID string, kind domain.ClaimKind, value string, since, until time.Time) ([]domain.Device, error) {
	if r == nil || r.tx == nil {
		return nil, fmt.Errorf("identity snapshot is unavailable")
	}
	return findRecentDevicesByClaim(ctx, r.tx, r.now, scopeID, kind, value, since, until, 3)
}
