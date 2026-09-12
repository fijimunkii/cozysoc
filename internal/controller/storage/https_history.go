package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
)

const (
	HTTPSHistoryWindow  = 24 * time.Hour
	MaxHTTPSHistoryRuns = 20
	MaxHTTPSHistoryScan = 256
)

var ErrHTTPSHistoryNotFound = errors.New("retained https run is not available in the enrolled scope")

type HTTPSHistoryQuery struct {
	ScopeID string // Controller-selected, never accepted from a native client.
	RunID   string // Optional exact reference; bypasses the recent-list window, not retention.
	AsOf    time.Time
}

type HTTPSHistoryPage struct {
	Runs          []httpsrun.RetainedRun
	ScanTruncated bool
	Truncated     bool
}

type httpsAuditRow struct {
	id, kind, actor, retention string
	at, expires                int64
	version                    int
	payload                    sql.NullString
}

// CASE bounds materialized payloads before decoding. Unrelated audit payloads
// are not loaded. Both list and exact-key reads use the same validated envelope.
const httpsHistoryColumns = `CASE WHEN length(CAST(id AS BLOB)) <= 256 THEN id END,
 CASE WHEN length(CAST(kind AS BLOB)) <= 128 THEN kind END,
 CASE WHEN length(CAST(actor AS BLOB)) <= 128 THEN actor END, occurred_at_ns, schema_version,
	CASE WHEN kind = 'https-run' AND length(CAST(payload AS BLOB)) <= 4096 THEN payload END,
	CASE WHEN length(CAST(retention_class AS BLOB)) <= 32 THEN retention_class END, expires_at_ns`

type httpsRowScanner interface{ Scan(...any) error }

func scanHTTPSAudit(scanner httpsRowScanner) (httpsAuditRow, error) {
	var row httpsAuditRow
	err := scanner.Scan(&row.id, &row.kind, &row.actor, &row.at, &row.version, &row.payload, &row.retention, &row.expires)
	return row, err
}

func (row httpsAuditRow) event() (httpsrun.Event, error) {
	if row.kind != HTTPSRunAuditKind || row.retention != string(domain.RetentionAudit) || !row.payload.Valid {
		return httpsrun.Event{}, httpsrun.ErrHistory
	}
	e, err := httpsrun.DecodeRetainedEvent([]byte(row.payload.String))
	actor := "controller"
	if e.State == "authorized" {
		actor = "local-os-user"
	}
	if err != nil || row.version != e.SchemaVersion || row.at != e.At.UnixNano() || row.actor != actor ||
		row.id != "audit.https-run."+e.RunID+"."+e.State {
		return httpsrun.Event{}, httpsrun.ErrHistory
	}
	return e, nil
}

// ReadHTTPSHistory uses a consistent SQLite snapshot. It never probes, writes,
// prunes, restores approval or synthesizes a missing phase. Exact lookups use at
// most three primary keys. The recent list inspects at most 256 indexed audit
// rows (INCLUDING unrelated/expired rows) and resolves at most 20 run triples.
// This deliberately exposes scan truncation instead of doing an unbounded JSON
// filter/group-by or silently calling a starved result an exhaustive empty list.
func (s *Store) ReadHTTPSHistory(ctx context.Context, q HTTPSHistoryQuery) (HTTPSHistoryPage, error) {
	if s == nil || s.gatewayHistoryDB == nil || s.now == nil || validateQueryID("scope", q.ScopeID) != nil ||
		q.AsOf.IsZero() || !time.Unix(0, q.AsOf.UnixNano()).Equal(q.AsOf) ||
		(q.RunID != "" && !httpsrun.ValidRunID(q.RunID)) {
		return HTTPSHistoryPage{}, httpsrun.ErrHistory
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	now := s.now().UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return HTTPSHistoryPage{}, httpsrun.ErrHistory
	}
	// A historical as-of can never resurrect a row whose retention already ended.
	retainedAt := max(now.UnixNano(), q.AsOf.UnixNano())
	since := q.AsOf.Add(-HTTPSHistoryWindow)
	if !time.Unix(0, since.UnixNano()).Equal(since) {
		return HTTPSHistoryPage{}, httpsrun.ErrHistory
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HTTPSHistoryPage{}, err
	}
	defer tx.Rollback()
	page, err := readHTTPSHistoryTx(ctx, tx, q, retainedAt, since)
	if err != nil {
		return HTTPSHistoryPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return HTTPSHistoryPage{}, err
	}
	return page, nil
}

func readHTTPSHistoryTx(ctx context.Context, tx *sql.Tx, q HTTPSHistoryQuery, retainedAt int64, since time.Time) (HTTPSHistoryPage, error) {
	scopes, err := listActiveDeviceWatchScopesTx(ctx, tx, 2)
	if err != nil {
		return HTTPSHistoryPage{}, err
	}
	if len(scopes) != 1 || scopes[0].ID != q.ScopeID {
		return HTTPSHistoryPage{}, ErrHTTPSHistoryNotFound
	}
	page := HTTPSHistoryPage{Runs: []httpsrun.RetainedRun{}}
	ids := []string{}
	if q.RunID != "" {
		ids = append(ids, q.RunID)
	} else {
		rows, err := tx.QueryContext(ctx, `SELECT `+httpsHistoryColumns+` FROM audit_events INDEXED BY audit_events_time_idx
			WHERE occurred_at_ns >= ? AND occurred_at_ns <= ?
			ORDER BY occurred_at_ns DESC, id ASC LIMIT ?`, since.UnixNano(), q.AsOf.UnixNano(), MaxHTTPSHistoryScan+1)
		if err != nil {
			return HTTPSHistoryPage{}, err
		}
		seen := make(map[string]bool, MaxHTTPSHistoryRuns)
		count := 0
		for rows.Next() {
			count++
			if count > MaxHTTPSHistoryScan {
				page.ScanTruncated = true
				break
			}
			row, err := scanHTTPSAudit(rows)
			if err != nil {
				rows.Close()
				return HTTPSHistoryPage{}, err
			}
			if row.kind != HTTPSRunAuditKind || row.expires <= retainedAt {
				continue
			}
			e, err := row.event()
			if err != nil {
				rows.Close()
				return HTTPSHistoryPage{}, err
			}
			if e.Observer.ScopeID != q.ScopeID || seen[e.RunID] {
				continue
			}
			if len(ids) == MaxHTTPSHistoryRuns {
				page.Truncated = true
				break
			}
			seen[e.RunID] = true
			ids = append(ids, e.RunID)
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return HTTPSHistoryPage{}, readErr
		}
		if closeErr != nil {
			return HTTPSHistoryPage{}, closeErr
		}
	}
	for _, id := range ids {
		events := make([]httpsrun.Event, 0, 3)
		for _, phase := range []string{"authorized", "admitted", "finished"} {
			row, err := scanHTTPSAudit(tx.QueryRowContext(ctx, `SELECT `+httpsHistoryColumns+` FROM audit_events WHERE id = ?`, "audit.https-run."+id+"."+phase))
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return HTTPSHistoryPage{}, err
			}
			if row.expires <= retainedAt || row.at > q.AsOf.UnixNano() {
				continue
			}
			e, err := row.event()
			if err != nil {
				return HTTPSHistoryPage{}, err
			}
			events = append(events, e)
		}
		if len(events) == 0 || events[0].Observer.ScopeID != q.ScopeID {
			return HTTPSHistoryPage{}, ErrHTTPSHistoryNotFound
		}
		run, err := httpsrun.DescribeRetainedRun(events, q.AsOf)
		if err != nil {
			return HTTPSHistoryPage{}, err
		}
		page.Runs = append(page.Runs, run)
	}
	return page, nil
}
