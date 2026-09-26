package storage

import (
	"context"
	"fmt"
)

// Inventory counts stored logical records, not SQLite pages or uncompressed
// evidence bytes. Batch entries may retain identity claims after an observation
// expires, so they are deliberately called evidence records.
type Inventory struct {
	BatchEvidenceRecords int64
	OtherObservations    int64
	IdentityClaims       int64
	CoverageSamples      int64
	Findings             int64
	AuditEvents          int64
	SavedCheckSelections int64
	LabeledDevices       int64
}

// Inventory reads one consistent SQLite snapshot on the read-only pool. It
// excludes transient files, user-saved exports, Keychain secrets and external
// services; callers must not infer their absence from a zero count.
func (s *Store) Inventory(ctx context.Context) (Inventory, error) {
	if s == nil || s.gatewayHistoryDB == nil {
		return Inventory{}, fmt.Errorf("storage inventory is unavailable")
	}
	var out Inventory
	err := s.gatewayHistoryDB.QueryRowContext(ctx, `SELECT
		(SELECT coalesce(sum(entries), 0) FROM evidence_batches),
		(SELECT count(*) FROM observations),
		(SELECT count(*) FROM identity_claims),
		(SELECT count(*) FROM coverage_samples),
		(SELECT count(*) FROM findings),
		(SELECT count(*) FROM audit_events),
		(SELECT count(*) FROM resolver_configurations) + (SELECT count(*) FROM https_configurations),
		(SELECT count(*) FROM devices WHERE user_label IS NOT NULL AND user_label != '')`).Scan(
		&out.BatchEvidenceRecords, &out.OtherObservations, &out.IdentityClaims,
		&out.CoverageSamples, &out.Findings, &out.AuditEvents,
		&out.SavedCheckSelections, &out.LabeledDevices)
	if err != nil {
		return Inventory{}, fmt.Errorf("read storage inventory: %w", err)
	}
	return out, nil
}
