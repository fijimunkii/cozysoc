package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const MaxRecentArrivalFindings = 100

type ArrivalFinding struct {
	ID                    string
	ScopeID               string
	ObservedAt            time.Time
	RecordedAt            time.Time
	EvidenceObservationID string
	EvidenceRetained      bool
}

type ArrivalFindingPage struct {
	Findings  []ArrivalFinding
	Truncated bool
}

// ListRecentArrivalFindings exposes only the fixed Device Watch informational
// producer. Other normalized findings need their own reviewed product copy.
// Expiry is evaluated at read time, including for the referenced observation.
func (s *Store) ListRecentArrivalFindings(ctx context.Context, asOf time.Time) (ArrivalFindingPage, error) {
	if s == nil || s.gatewayHistoryDB == nil || asOf.IsZero() {
		return ArrivalFindingPage{}, fmt.Errorf("arrival findings are unavailable")
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ArrivalFindingPage{}, fmt.Errorf("begin arrival finding read: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT f.id,f.scope_id,f.observed_at_ns,f.created_at_ns,e.observation_id
		FROM findings f JOIN finding_evidence e ON e.finding_id=f.id
		WHERE f.detector_id='device-watch-arrival' AND f.detector_version='1'
		AND f.category='new-device' AND f.severity='informational'
		AND f.expires_at_ns>? AND f.created_at_ns<=?
		ORDER BY f.created_at_ns DESC,f.id DESC LIMIT ?`, asOf.UnixNano(), asOf.UnixNano(), MaxRecentArrivalFindings+1)
	if err != nil {
		return ArrivalFindingPage{}, fmt.Errorf("read arrival findings: %w", err)
	}
	page := ArrivalFindingPage{Findings: make([]ArrivalFinding, 0, MaxRecentArrivalFindings)}
	for rows.Next() {
		var item ArrivalFinding
		var observed, recorded int64
		if err := rows.Scan(&item.ID, &item.ScopeID, &observed, &recorded, &item.EvidenceObservationID); err != nil {
			rows.Close()
			return ArrivalFindingPage{}, fmt.Errorf("scan arrival finding: %w", err)
		}
		if err := validateQueryID("finding id", item.ID); err != nil {
			rows.Close()
			return ArrivalFindingPage{}, err
		}
		if err := validateQueryID("finding scope", item.ScopeID); err != nil {
			rows.Close()
			return ArrivalFindingPage{}, err
		}
		if err := validateQueryID("finding observation", item.EvidenceObservationID); err != nil {
			rows.Close()
			return ArrivalFindingPage{}, err
		}
		item.ObservedAt = time.Unix(0, observed).UTC()
		item.RecordedAt = time.Unix(0, recorded).UTC()
		page.Findings = append(page.Findings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ArrivalFindingPage{}, fmt.Errorf("iterate arrival findings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ArrivalFindingPage{}, fmt.Errorf("close arrival findings: %w", err)
	}
	if len(page.Findings) > MaxRecentArrivalFindings {
		page.Findings = page.Findings[:MaxRecentArrivalFindings]
		page.Truncated = true
	}
	for n := range page.Findings {
		item := &page.Findings[n]
		var retained int
		err := tx.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM observations WHERE id=? AND expires_at_ns>?) OR
			EXISTS(SELECT 1 FROM evidence_batch_lookup WHERE id=? AND expires_at_ns>?)`,
			item.EvidenceObservationID, asOf.UnixNano(), packedEvidenceKey(item.EvidenceObservationID, "obs.dw."), asOf.UnixNano()).Scan(&retained)
		if err != nil {
			return ArrivalFindingPage{}, fmt.Errorf("read arrival evidence availability: %w", err)
		}
		item.EvidenceRetained = retained == 1
	}
	if err := tx.Commit(); err != nil {
		return ArrivalFindingPage{}, fmt.Errorf("finish arrival finding read: %w", err)
	}
	return page, nil
}
