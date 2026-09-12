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
	page, err := s.readQualityHistory(ctx, scopeID, asOf, false)
	return page.QualityHistoryPage, err
}

// QualityHistoryWithHTTPSPage adds selected HTTPS history to the same snapshot.
// Each layer keeps its own bounded scan and completeness flags. This is retained
// evidence, not a diagnosis or proof that the observations can be compared.
type QualityHistoryWithHTTPSPage struct {
	QualityHistoryPage
	HTTPS HTTPSHistoryPage
}

// ReadQualityHistoryWithHTTPS is the three-layer storage input for a future
// retained comparison adapter. The existing two-layer read remains independent
// of HTTPS audit validity and does not pay for an unused HTTPS scan.
func (s *Store) ReadQualityHistoryWithHTTPS(ctx context.Context, scopeID string, asOf time.Time) (QualityHistoryWithHTTPSPage, error) {
	return s.readQualityHistory(ctx, scopeID, asOf, true)
}

func (s *Store) readQualityHistory(ctx context.Context, scopeID string, asOf time.Time, includeHTTPS bool) (QualityHistoryWithHTTPSPage, error) {
	if s == nil || s.gatewayHistoryDB == nil || s.now == nil || validateQueryID("scope", scopeID) != nil ||
		asOf.IsZero() || !time.Unix(0, asOf.UnixNano()).Equal(asOf) {
		return QualityHistoryWithHTTPSPage{}, ErrQualityHistory
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	now := s.now().UTC()
	since := asOf.Add(-GatewayHistoryWindow)
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) || !time.Unix(0, since.UnixNano()).Equal(since) {
		return QualityHistoryWithHTTPSPage{}, ErrQualityHistory
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return QualityHistoryWithHTTPSPage{}, err
	}
	defer tx.Rollback()
	retainedAt := max(now.UnixNano(), asOf.UnixNano())
	gateway, err := readGatewayHistoryTx(ctx, tx, GatewayHistoryQuery{ScopeID: scopeID, AsOf: asOf}, retainedAt, since)
	if err != nil {
		return QualityHistoryWithHTTPSPage{}, err
	}
	resolver, err := readResolverHistoryTx(ctx, tx, ResolverHistoryQuery{ScopeID: scopeID, AsOf: asOf}, retainedAt, since)
	if err != nil {
		return QualityHistoryWithHTTPSPage{}, err
	}
	page := QualityHistoryWithHTTPSPage{QualityHistoryPage: QualityHistoryPage{Gateway: gateway, Resolver: resolver}}
	if includeHTTPS {
		page.HTTPS, err = readHTTPSHistoryTx(ctx, tx, HTTPSHistoryQuery{ScopeID: scopeID, AsOf: asOf}, retainedAt, asOf.Add(-HTTPSHistoryWindow))
		if err != nil {
			return QualityHistoryWithHTTPSPage{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return QualityHistoryWithHTTPSPage{}, err
	}
	return page, nil
}
