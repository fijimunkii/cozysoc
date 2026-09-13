package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"sort"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// Groups are routing dictionaries, not evidence intervals. Every query must
// inspect the original claims in selected batches before accepting a match.
const evidenceBatchIdentitySchema = `
CREATE TABLE evidence_batch_identity_groups (
 id INTEGER PRIMARY KEY,
 source_id INTEGER NOT NULL REFERENCES evidence_batch_sources(id),
 routing BLOB NOT NULL CHECK(length(routing) BETWEEN 2 AND 16384),
 UNIQUE(source_id,routing)
) STRICT;
CREATE TABLE evidence_batch_identity_routes (
 group_id INTEGER NOT NULL REFERENCES evidence_batch_identity_groups(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(length(kind) BETWEEN 1 AND 64),
 value TEXT NOT NULL CHECK(length(value) BETWEEN 1 AND 512),
 device_id TEXT NOT NULL CHECK(length(device_id) BETWEEN 1 AND 128),
 PRIMARY KEY(group_id,kind,value,device_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX evidence_batch_identity_value ON evidence_batch_identity_routes(kind,value,device_id,group_id);
`

type evidenceBatchIdentityRoute struct {
	Kind            domain.ClaimKind
	Value, DeviceID string
}

func evidenceBatchRouting(r EvidenceBatchRecord) ([]byte, []evidenceBatchIdentityRoute, error) {
	claims := make(map[string]domain.IdentityClaim, len(r.Claims))
	for _, c := range r.Claims {
		claims[c.Claim.ID] = c.Claim
	}
	seen := make(map[evidenceBatchIdentityRoute]bool, len(r.Links))
	routes := make([]evidenceBatchIdentityRoute, 0, len(r.Links))
	for _, l := range r.Links {
		c, ok := claims[l.ClaimID]
		if !ok {
			return nil, nil, ErrEvidenceBatchData
		}
		value, err := domain.NormalizeClaimValue(c.Kind, c.Value)
		if err != nil {
			return nil, nil, err
		}
		route := evidenceBatchIdentityRoute{c.Kind, value, l.DeviceID}
		if !seen[route] {
			seen[route] = true
			routes = append(routes, route)
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Value != b.Value {
			return a.Value < b.Value
		}
		return a.DeviceID < b.DeviceID
	})
	key, err := json.Marshal(routes)
	if err != nil {
		return nil, nil, err
	}
	if len(key) > 16384 {
		return nil, nil, ErrEvidenceBatchLimit
	}
	return key, routes, nil
}

func ensureEvidenceBatchIdentityGroup(ctx context.Context, tx *sql.Tx, source int64, r EvidenceBatchRecord) (int64, error) {
	key, routes, err := evidenceBatchRouting(r)
	if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO evidence_batch_identity_groups(source_id,routing) VALUES(?,?) ON CONFLICT(source_id,routing) DO NOTHING`, source, key)
	if err != nil {
		return 0, err
	}
	var group int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM evidence_batch_identity_groups WHERE source_id=? AND routing=?`, source, key).Scan(&group); err != nil {
		return 0, err
	}
	// Only newly created groups may receive their routes; validate existing groups
	// rather than silently repairing an incomplete index.
	inserted, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if inserted != 0 {
		for _, route := range routes {
			if _, err = tx.ExecContext(ctx, `INSERT INTO evidence_batch_identity_routes(group_id,kind,value,device_id) VALUES(?,?,?,?)`, group, route.Kind, route.Value, route.DeviceID); err != nil {
				return 0, err
			}
		}
	}
	return group, validateEvidenceBatchIdentityGroup(ctx, tx, group, source, []EvidenceBatchRecord{r})
}

func validateEvidenceBatchIdentityGroup(ctx context.Context, tx *sql.Tx, group, source int64, records []EvidenceBatchRecord) error {
	var stored []byte
	if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN length(routing)<=16384 THEN routing ELSE NULL END FROM evidence_batch_identity_groups WHERE id=? AND source_id=?`, group, source).Scan(&stored); err != nil {
		return err
	}
	var expected []evidenceBatchIdentityRoute
	for _, r := range records {
		key, routes, err := evidenceBatchRouting(r)
		if err != nil {
			return err
		}
		if !bytes.Equal(stored, key) {
			return ErrEvidenceBatchData
		}
		expected = routes
	}
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN length(kind)<=64 THEN kind ELSE NULL END,CASE WHEN length(value)<=512 THEN value ELSE NULL END,CASE WHEN length(device_id)<=128 THEN device_id ELSE NULL END FROM evidence_batch_identity_routes WHERE group_id=? ORDER BY kind,value,device_id LIMIT 9`, group)
	if err != nil {
		return err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var route evidenceBatchIdentityRoute
		if err := rows.Scan(&route.Kind, &route.Value, &route.DeviceID); err != nil {
			return err
		}
		if i >= len(expected) || route != expected[i] {
			return ErrEvidenceBatchData
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if i != len(expected) {
		return ErrEvidenceBatchData
	}
	return nil
}

func removeUnusedEvidenceBatchIdentityGroup(ctx context.Context, tx *sql.Tx, group int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM evidence_batch_identity_groups WHERE id=? AND NOT EXISTS(SELECT 1 FROM evidence_batches WHERE identity_group=? LIMIT 1)`, group, group)
	return err
}

// Retention can give records formerly sharing a group different surviving keys.
// Partition by exact routing first, then apply the existing byte/record bounds.
func partitionIdentityEvidence(records []EvidenceBatchRecord) ([][]EvidenceBatchRecord, error) {
	positions := map[string]int{}
	groups := make([][]EvidenceBatchRecord, 0)
	for _, r := range records {
		key, _, err := evidenceBatchRouting(r)
		if err != nil {
			return nil, err
		}
		i, ok := positions[string(key)]
		if !ok {
			i = len(groups)
			positions[string(key)] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], r)
	}
	var result [][]EvidenceBatchRecord
	for _, group := range groups {
		parts, err := partitionRetainedEvidence(group)
		if err != nil {
			return nil, err
		}
		result = append(result, parts...)
	}
	return result, nil
}

// These bounds select candidates only; they never establish an observation in
// a gap or extend a claim's individual expiry.
func evidenceBatchClaimBounds(records []EvidenceBatchRecord) (first, last, expiry int64) {
	seen := false
	for _, r := range records {
		for _, c := range r.Claims {
			at := c.Claim.ObservedAt.UnixNano()
			ex := c.ExpiresAt.UnixNano()
			if !seen || at < first {
				first = at
			}
			if !seen || at > last {
				last = at
			}
			if !seen || ex > expiry {
				expiry = ex
			}
			seen = true
		}
	}
	return
}

func writeEvidenceBatchClaimBounds(ctx context.Context, tx *sql.Tx, batch int64, records []EvidenceBatchRecord) error {
	first, last, expiry := evidenceBatchClaimBounds(records)
	_, err := tx.ExecContext(ctx, `UPDATE evidence_batches SET first_claim_ns=?,last_claim_ns=?,last_claim_expiry_ns=? WHERE id=?`, first, last, expiry, batch)
	return err
}

func validateEvidenceBatchClaimTimes(records []EvidenceBatchRecord) error {
	for _, r := range records {
		for _, c := range r.Claims {
			if !batchTimeFits(c.Claim.ObservedAt) {
				return ErrEvidenceBatchData
			}
		}
	}
	return nil
}

func validateEvidenceBatchClaimBounds(ctx context.Context, tx *sql.Tx, batch int64, records []EvidenceBatchRecord) error {
	if err := validateEvidenceBatchClaimTimes(records); err != nil {
		return err
	}
	var first, last, expiry int64
	if err := tx.QueryRowContext(ctx, `SELECT first_claim_ns,last_claim_ns,last_claim_expiry_ns FROM evidence_batches WHERE id=?`, batch).Scan(&first, &last, &expiry); err != nil {
		return err
	}
	f, l, e := evidenceBatchClaimBounds(records)
	if f != first || l != last || e != expiry {
		return ErrEvidenceBatchData
	}
	return nil
}
