package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrQualityHistory = errors.New("retained quality history is unavailable")

// QualityHistoryPage contains both bounded histories from one read-only SQLite
// snapshot. It does not collect current interface metadata or recreate approval.
type QualityHistoryPage struct {
	Gateway  GatewayHistoryPage
	Resolver ResolverHistoryPage
}

func (s *Store) ReadQualityHistory(ctx context.Context, scopeID string, asOf time.Time) (QualityHistoryPage, error) {
	if s == nil || s.gatewayHistoryDB == nil || s.now == nil || validateQueryID("scope", scopeID) != nil ||
		asOf.IsZero() || !time.Unix(0, asOf.UnixNano()).Equal(asOf) {
		return QualityHistoryPage{}, ErrQualityHistory
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	now := s.now().UTC()
	since := asOf.Add(-GatewayHistoryWindow)
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) || !time.Unix(0, since.UnixNano()).Equal(since) {
		return QualityHistoryPage{}, ErrQualityHistory
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return QualityHistoryPage{}, err
	}
	defer tx.Rollback()
	retainedAt := max(now.UnixNano(), asOf.UnixNano())
	gateway, err := readGatewayHistoryTx(ctx, tx, GatewayHistoryQuery{ScopeID: scopeID, AsOf: asOf}, retainedAt, since)
	if err != nil {
		return QualityHistoryPage{}, err
	}
	resolver, err := readResolverHistoryTx(ctx, tx, ResolverHistoryQuery{ScopeID: scopeID, AsOf: asOf}, retainedAt, since)
	if err != nil {
		return QualityHistoryPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return QualityHistoryPage{}, err
	}
	return QualityHistoryPage{Gateway: gateway, Resolver: resolver}, nil
}
