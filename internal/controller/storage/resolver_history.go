package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
)

const (
	ResolverHistoryWindow  = 24 * time.Hour
	MaxResolverHistoryRuns = 20
	MaxResolverHistoryScan = 256
)

var ErrResolverHistoryNotFound = errors.New("retained resolver run is not available in the enrolled scope")

type ResolverHistoryQuery struct {
	ScopeID string // Controller-selected, never accepted from a native client.
	RunID   string // Optional exact reference; bypasses the recent-list window, not retention.
	AsOf    time.Time
}

type ResolverHistoryPage struct {
	Runs          []resolverrun.RetainedRun
	ScanTruncated bool
	Truncated     bool
}

type resolverAuditRow struct {
	id, kind, actor, retention string
	at, expires                int64
	version                    int
	payload                    sql.NullString
}

// CASE bounds materialized payloads before decoding. Unrelated audit payloads
// are not loaded. Both list and exact-key reads use the same validated envelope.
const resolverHistoryColumns = `CASE WHEN length(CAST(id AS BLOB)) <= 256 THEN id END,
 CASE WHEN length(CAST(kind AS BLOB)) <= 128 THEN kind END,
 CASE WHEN length(CAST(actor AS BLOB)) <= 128 THEN actor END, occurred_at_ns, schema_version,
	CASE WHEN kind = 'resolver-run' AND length(CAST(payload AS BLOB)) <= 4096 THEN payload END,
	CASE WHEN length(CAST(retention_class AS BLOB)) <= 32 THEN retention_class END, expires_at_ns`

type resolverRowScanner interface{ Scan(...any) error }

func scanResolverAudit(scanner resolverRowScanner) (resolverAuditRow, error) {
	var row resolverAuditRow
	err := scanner.Scan(&row.id, &row.kind, &row.actor, &row.at, &row.version, &row.payload, &row.retention, &row.expires)
	return row, err
}

func (row resolverAuditRow) event() (resolverrun.Event, error) {
	if row.kind != ResolverRunAuditKind || row.retention != string(domain.RetentionAudit) || !row.payload.Valid {
		return resolverrun.Event{}, resolverrun.ErrHistory
	}
	e, err := resolverrun.DecodeRetainedEvent([]byte(row.payload.String))
	actor := "controller"
	if e.State == "authorized" {
		actor = "local-os-user"
	}
	if err != nil || row.version != e.SchemaVersion || row.at != e.At.UnixNano() || row.actor != actor ||
		row.id != "audit.resolver-run."+e.RunID+"."+e.State {
		return resolverrun.Event{}, resolverrun.ErrHistory
	}
	return e, nil
}

// ReadResolverHistory uses a consistent SQLite snapshot. It never probes, writes,
// prunes, restores approval or synthesizes a missing phase. Exact lookups use at
// most three primary keys. The recent list inspects at most 256 indexed audit
// rows (INCLUDING unrelated/expired rows) and resolves at most 20 run triples.
// This deliberately exposes scan truncation instead of doing an unbounded JSON
// filter/group-by or silently calling a starved result an exhaustive empty list.
func (s *Store) ReadResolverHistory(ctx context.Context, q ResolverHistoryQuery) (ResolverHistoryPage, error) {
	if s == nil || s.gatewayHistoryDB == nil || s.now == nil || validateQueryID("scope", q.ScopeID) != nil ||
		q.AsOf.IsZero() || !time.Unix(0, q.AsOf.UnixNano()).Equal(q.AsOf) ||
		(q.RunID != "" && !resolverrun.ValidRunID(q.RunID)) {
		return ResolverHistoryPage{}, resolverrun.ErrHistory
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	now := s.now().UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return ResolverHistoryPage{}, resolverrun.ErrHistory
	}
	// A historical as-of can never resurrect a row whose retention already ended.
	retainedAt := max(now.UnixNano(), q.AsOf.UnixNano())
	since := q.AsOf.Add(-ResolverHistoryWindow)
	if !time.Unix(0, since.UnixNano()).Equal(since) {
		return ResolverHistoryPage{}, resolverrun.ErrHistory
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ResolverHistoryPage{}, err
	}
	defer tx.Rollback()
	scopes, err := listActiveDeviceWatchScopesTx(ctx, tx, 2)
	if err != nil {
		return ResolverHistoryPage{}, err
	}
	if len(scopes) != 1 || scopes[0].ID != q.ScopeID {
		return ResolverHistoryPage{}, ErrResolverHistoryNotFound
	}
	page := ResolverHistoryPage{Runs: []resolverrun.RetainedRun{}}
	ids := []string{}
	if q.RunID != "" {
		ids = append(ids, q.RunID)
	} else {
		rows, err := tx.QueryContext(ctx, `SELECT `+resolverHistoryColumns+` FROM audit_events INDEXED BY audit_events_time_idx
			WHERE occurred_at_ns >= ? AND occurred_at_ns <= ?
			ORDER BY occurred_at_ns DESC, id ASC LIMIT ?`, since.UnixNano(), q.AsOf.UnixNano(), MaxResolverHistoryScan+1)
		if err != nil {
			return ResolverHistoryPage{}, err
		}
		seen := make(map[string]bool, MaxResolverHistoryRuns)
		count := 0
		for rows.Next() {
			count++
			if count > MaxResolverHistoryScan {
				page.ScanTruncated = true
				break
			}
			row, err := scanResolverAudit(rows)
			if err != nil {
				rows.Close()
				return ResolverHistoryPage{}, err
			}
			if row.kind != ResolverRunAuditKind || row.expires <= retainedAt {
				continue
			}
			e, err := row.event()
			if err != nil {
				rows.Close()
				return ResolverHistoryPage{}, err
			}
			if e.Observer.ScopeID != q.ScopeID || seen[e.RunID] {
				continue
			}
			if len(ids) == MaxResolverHistoryRuns {
				page.Truncated = true
				break
			}
			seen[e.RunID] = true
			ids = append(ids, e.RunID)
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return ResolverHistoryPage{}, readErr
		}
		if closeErr != nil {
			return ResolverHistoryPage{}, closeErr
		}
	}
	for _, id := range ids {
		events := make([]resolverrun.Event, 0, 3)
		for _, phase := range []string{"authorized", "admitted", "finished"} {
			row, err := scanResolverAudit(tx.QueryRowContext(ctx, `SELECT `+resolverHistoryColumns+` FROM audit_events WHERE id = ?`, "audit.resolver-run."+id+"."+phase))
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return ResolverHistoryPage{}, err
			}
			if row.expires <= retainedAt || row.at > q.AsOf.UnixNano() {
				continue
			}
			e, err := row.event()
			if err != nil {
				return ResolverHistoryPage{}, err
			}
			events = append(events, e)
		}
		if len(events) == 0 || events[0].Observer.ScopeID != q.ScopeID {
			return ResolverHistoryPage{}, ErrResolverHistoryNotFound
		}
		run, err := resolverrun.DescribeRetainedRun(events, q.AsOf)
		if err != nil {
			return ResolverHistoryPage{}, err
		}
		page.Runs = append(page.Runs, run)
	}
	if err := tx.Commit(); err != nil {
		return ResolverHistoryPage{}, err
	}
	return page, nil
}
