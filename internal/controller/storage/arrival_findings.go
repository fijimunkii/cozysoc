package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const MaxRecentArrivalFindings = 100

type ArrivalFinding struct {
	ID                    string
	ScopeID               string
	ObservedAt            time.Time
	RecordedAt            time.Time
	EvidenceObservationID string
	EvidenceRetained      bool
	AcknowledgedAt        *time.Time
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
	rows, err := tx.QueryContext(ctx, `SELECT f.id,f.scope_id,f.observed_at_ns,f.created_at_ns,e.observation_id,f.acknowledged_at_ns
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
		var acknowledged sql.NullInt64
		if err := rows.Scan(&item.ID, &item.ScopeID, &observed, &recorded, &item.EvidenceObservationID, &acknowledged); err != nil {
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
		if acknowledged.Valid && acknowledged.Int64 <= asOf.UnixNano() {
			at := time.Unix(0, acknowledged.Int64).UTC()
			item.AcknowledgedAt = &at
		}
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

var ErrArrivalFindingNotFound = errors.New("arrival finding is not available")

// Acknowledgement means a local user has reviewed this retained informational
// finding. It does not validate the inferred identity or suppress future data.
func (s *Store) AcknowledgeArrivalFinding(ctx context.Context, findingID string) (time.Time, bool, error) {
	if s == nil || s.conn == nil {
		return time.Time{}, false, fmt.Errorf("storage is unavailable")
	}
	if err := validateQueryID("finding id", findingID); err != nil {
		return time.Time{}, false, err
	}
	now := s.now().UTC()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("begin arrival acknowledgement: %w", err)
	}
	defer tx.Rollback()
	var acknowledged sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT acknowledged_at_ns FROM findings
		WHERE id=? AND detector_id='device-watch-arrival' AND detector_version='1'
		AND category='new-device' AND severity='informational' AND expires_at_ns>?`, findingID, now.UnixNano()).Scan(&acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, ErrArrivalFindingNotFound
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read arrival finding: %w", err)
	}
	if acknowledged.Valid {
		return time.Unix(0, acknowledged.Int64).UTC(), false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET acknowledged_at_ns=? WHERE id=? AND acknowledged_at_ns IS NULL`, now.UnixNano(), findingID); err != nil {
		return time.Time{}, false, wrapWrite("acknowledge arrival finding", err)
	}
	payload, err := json.Marshal(map[string]any{"schema_version": 1, "finding_id": findingID, "state": "acknowledged"})
	if err != nil {
		return time.Time{}, false, fmt.Errorf("encode arrival acknowledgement: %w", err)
	}
	auditID, err := randomID("audit.finding-acknowledgement")
	if err != nil {
		return time.Time{}, false, err
	}
	event := domain.AuditEvent{ID: auditID, Kind: "finding-acknowledgement", Actor: "local-os-user", OccurredAt: now, SchemaVersion: 1, Payload: payload, Retention: domain.RetentionAudit}
	if err := domain.ValidateAuditEvent(event); err != nil {
		return time.Time{}, false, err
	}
	expiresAt, err := s.expiry(domain.RetentionAudit)
	if err != nil {
		return time.Time{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events
		(id,kind,actor,occurred_at_ns,schema_version,payload,retention_class,expires_at_ns)
		VALUES (?,?,?,?,?,?,?,?)`, event.ID, event.Kind, event.Actor, now.UnixNano(), event.SchemaVersion, string(event.Payload), event.Retention, expiresAt); err != nil {
		return time.Time{}, false, wrapWrite("audit arrival acknowledgement", err)
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, false, wrapWrite("commit arrival acknowledgement", err)
	}
	return now, true, nil
}
