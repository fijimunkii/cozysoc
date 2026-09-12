package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
)

const (
	GatewayHistoryWindow  = 24 * time.Hour
	MaxGatewayHistoryRuns = 20
	MaxGatewayHistoryScan = 256
)

var ErrGatewayHistoryNotFound = errors.New("retained gateway run is not available in the enrolled scope")

type GatewayHistoryQuery struct {
	ScopeID string // Controller-selected, never accepted from a native client.
	RunID   string // Optional exact reference; bypasses the recent-list window, not retention.
	AsOf    time.Time
}

type GatewayHistoryPage struct {
	Runs          []gatewayrun.RetainedRun
	ScanTruncated bool
	Truncated     bool
}

type gatewayAuditRow struct {
	id, kind, actor, retention string
	at, expires                int64
	version                    int
	payload                    sql.NullString
}

// CASE bounds materialized payloads before decoding. Unrelated audit payloads
// are not loaded. Both list and exact-key reads use the same validated envelope.
const gatewayHistoryColumns = `id, kind, actor, occurred_at_ns, schema_version,
	CASE WHEN kind = 'gateway-run' AND length(CAST(payload AS BLOB)) <= 4096 THEN payload END,
	retention_class, expires_at_ns`

type gatewayRowScanner interface{ Scan(...any) error }

func scanGatewayAudit(scanner gatewayRowScanner) (gatewayAuditRow, error) {
	var row gatewayAuditRow
	err := scanner.Scan(&row.id, &row.kind, &row.actor, &row.at, &row.version, &row.payload, &row.retention, &row.expires)
	return row, err
}

func (row gatewayAuditRow) event() (gatewayrun.Event, error) {
	if row.kind != GatewayRunAuditKind || row.retention != string(domain.RetentionAudit) || !row.payload.Valid {
		return gatewayrun.Event{}, gatewayrun.ErrHistory
	}
	e, err := gatewayrun.DecodeRetainedEvent([]byte(row.payload.String))
	actor := "controller"
	if e.State == "authorized" {
		actor = "local-os-user"
	}
	if err != nil || row.version != e.SchemaVersion || row.at != e.At.UnixNano() || row.actor != actor ||
		row.id != "audit.gateway-run."+e.RunID+"."+e.State {
		return gatewayrun.Event{}, gatewayrun.ErrHistory
	}
	return e, nil
}

// ReadGatewayHistory uses a consistent SQLite snapshot. It never probes, writes,
// prunes, restores approval or synthesizes a missing phase. Exact lookups use at
// most three primary keys. The recent list inspects at most 256 indexed audit
// rows (INCLUDING unrelated/expired rows) and resolves at most 20 run triples.
// This deliberately exposes scan truncation instead of doing an unbounded JSON
// filter/group-by or silently calling a starved result an exhaustive empty list.
func (s *Store) ReadGatewayHistory(ctx context.Context, q GatewayHistoryQuery) (GatewayHistoryPage, error) {
	if s == nil || s.gatewayHistoryDB == nil || s.now == nil || validateQueryID("scope", q.ScopeID) != nil ||
		q.AsOf.IsZero() || !time.Unix(0, q.AsOf.UnixNano()).Equal(q.AsOf) ||
		(q.RunID != "" && !gatewayrun.ValidRunID(q.RunID)) {
		return GatewayHistoryPage{}, gatewayrun.ErrHistory
	}
	now := s.now().UTC()
	if now.IsZero() || !time.Unix(0, now.UnixNano()).Equal(now) {
		return GatewayHistoryPage{}, gatewayrun.ErrHistory
	}
	// A historical as-of can never resurrect a row whose retention already ended.
	retainedAt := max(now.UnixNano(), q.AsOf.UnixNano())
	since := q.AsOf.Add(-GatewayHistoryWindow)
	if !time.Unix(0, since.UnixNano()).Equal(since) {
		return GatewayHistoryPage{}, gatewayrun.ErrHistory
	}
	tx, err := s.gatewayHistoryDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return GatewayHistoryPage{}, err
	}
	defer tx.Rollback()
	page, err := readGatewayHistoryTx(ctx, tx, q, retainedAt, since)
	if err != nil {
		return GatewayHistoryPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return GatewayHistoryPage{}, err
	}
	return page, nil
}

func readGatewayHistoryTx(ctx context.Context, tx *sql.Tx, q GatewayHistoryQuery, retainedAt int64, since time.Time) (GatewayHistoryPage, error) {
	scopes, err := listActiveDeviceWatchScopesTx(ctx, tx, 2)
	if err != nil {
		return GatewayHistoryPage{}, err
	}
	if len(scopes) != 1 || scopes[0].ID != q.ScopeID {
		return GatewayHistoryPage{}, ErrGatewayHistoryNotFound
	}
	page := GatewayHistoryPage{Runs: []gatewayrun.RetainedRun{}}
	ids := []string{}
	if q.RunID != "" {
		ids = append(ids, q.RunID)
	} else {
		rows, err := tx.QueryContext(ctx, `SELECT `+gatewayHistoryColumns+` FROM audit_events INDEXED BY audit_events_time_idx
			WHERE occurred_at_ns >= ? AND occurred_at_ns <= ?
			ORDER BY occurred_at_ns DESC, id ASC LIMIT ?`, since.UnixNano(), q.AsOf.UnixNano(), MaxGatewayHistoryScan+1)
		if err != nil {
			return GatewayHistoryPage{}, err
		}
		seen := make(map[string]bool, MaxGatewayHistoryRuns)
		count := 0
		for rows.Next() {
			count++
			if count > MaxGatewayHistoryScan {
				page.ScanTruncated = true
				break
			}
			row, err := scanGatewayAudit(rows)
			if err != nil {
				rows.Close()
				return GatewayHistoryPage{}, err
			}
			if row.kind != GatewayRunAuditKind || row.expires <= retainedAt {
				continue
			}
			e, err := row.event()
			if err != nil {
				rows.Close()
				return GatewayHistoryPage{}, err
			}
			if e.ScopeID != q.ScopeID || seen[e.RunID] {
				continue
			}
			if len(ids) == MaxGatewayHistoryRuns {
				page.Truncated = true
				break
			}
			seen[e.RunID] = true
			ids = append(ids, e.RunID)
		}
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return GatewayHistoryPage{}, readErr
		}
		if closeErr != nil {
			return GatewayHistoryPage{}, closeErr
		}
	}
	for _, id := range ids {
		events := make([]gatewayrun.Event, 0, 3)
		for _, phase := range []string{"authorized", "admitted", "finished"} {
			row, err := scanGatewayAudit(tx.QueryRowContext(ctx, `SELECT `+gatewayHistoryColumns+` FROM audit_events WHERE id = ?`, "audit.gateway-run."+id+"."+phase))
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return GatewayHistoryPage{}, err
			}
			if row.expires <= retainedAt || row.at > q.AsOf.UnixNano() {
				continue
			}
			e, err := row.event()
			if err != nil {
				return GatewayHistoryPage{}, err
			}
			events = append(events, e)
		}
		if len(events) == 0 || events[0].ScopeID != q.ScopeID {
			return GatewayHistoryPage{}, ErrGatewayHistoryNotFound
		}
		run, err := gatewayrun.DescribeRetainedRun(events, q.AsOf)
		if err != nil {
			return GatewayHistoryPage{}, err
		}
		page.Runs = append(page.Runs, run)
	}
	return page, nil
}

// Open a lazy, separately owned read-only pool. There is at most one historical
// read connection per Store, with context-bounded queueing. Never use immutable=1:
// the writer remains live and normal SQLite snapshot/locking semantics apply.
func openGatewayHistoryDB(path string) (*sql.DB, error) {
	dsn, err := sqliteFileURI(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}
