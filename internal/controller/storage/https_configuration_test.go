package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/httpsplan"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

func httpsSettings() httpsplan.Configuration {
	return httpsplan.Configuration{Selection: nq.HTTPSSelection{Family: nq.FamilyIPv4, Method: "HEAD", ExpectedStatus: 204}, Endpoint: netip.MustParseAddrPort("198.51.100.20:443"), ServerName: "Private.Example", RequestTarget: "/private-check?test=1", DestinationPolicy: httpsplan.ExactEndpoint}
}
func TestHTTPSConfigurationRestartRetirementAndRedaction(t *testing.T) {
	ctx := context.Background()
	s, dir := settingsStore(t)
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	original := saved.Disclosure()
	if original.ServerName != "private.example" || original.Selection.ID == "" || original.Selection.RequestID == "" || original.Selection.EndpointID == "" {
		t.Fatalf("invalid disclosure: %v", saved)
	}
	copy := saved.Disclosure()
	copy.ServerName = "changed.example"
	if saved.Disclosure() != original {
		t.Fatal("disclosure aliases record")
	}
	encoded, _ := json.Marshal(saved)
	for _, value := range []string{string(encoded), fmt.Sprintf("%v %+v %#v", saved, saved, saved)} {
		if strings.Contains(value, "private.example") || strings.Contains(value, "198.51.100.20") || strings.Contains(value, "/private-check") {
			t.Fatal("generic output leaked settings")
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	listed, err := s.ListActiveHTTPSConfigurations(ctx, "scope.home")
	if err != nil || len(listed) != 1 || listed[0].Disclosure() != original {
		t.Fatal("restart list lost settings")
	}
	loaded, err := s.ActiveHTTPSConfiguration(ctx, original.Selection.ID)
	if err != nil || loaded.Disclosure() != original || loaded.ScopeID != "scope.home" || !loaded.CreatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("restart changed settings: %v", err)
	}
	if err := s.RetireHTTPSConfiguration(ctx, original.Selection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveHTTPSConfiguration(ctx, original.Selection.ID); err == nil {
		t.Fatal("retired record resolved")
	}
	if err := s.RetireHTTPSConfiguration(ctx, original.Selection.ID); err == nil {
		t.Fatal("retirement replay succeeded")
	}
	listed, err = s.ListActiveHTTPSConfigurations(ctx, "scope.home")
	if err != nil || len(listed) != 0 {
		t.Fatal("list retained retired settings")
	}
	next := httpsSettings()
	next.ServerName = "other.example"
	next.Endpoint = netip.MustParseAddrPort("198.51.100.21:443")
	replacement, err := s.CreateHTTPSConfiguration(ctx, "scope.home", next)
	if err != nil {
		t.Fatal(err)
	}
	refs := replacement.Disclosure().Selection
	if refs.ID == original.Selection.ID || refs.EndpointID == original.Selection.EndpointID || refs.RequestID == original.Selection.RequestID {
		t.Fatal("replacement reused references")
	}
	var raw string
	if err := s.conn.QueryRowContext(ctx, `SELECT configuration FROM https_configurations WHERE id=?`, original.Selection.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var retained httpsplan.DisclosedConfiguration
	if json.Unmarshal([]byte(raw), &retained) != nil || httpsplan.Configuration(retained) != original {
		t.Fatal("old attribution changed")
	}
	rows, err := s.conn.QueryContext(ctx, `SELECT payload FROM audit_events WHERE kind=?`, HTTPSConfigurationAuditKind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		count++
		if strings.Contains(payload, "example") || strings.Contains(payload, "198.51.100") || strings.Contains(payload, "/private-check") {
			t.Fatal("audit leaked endpoint/name")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("audits = %d", count)
	}
	if settingsCount(t, s, "observations") != 0 || settingsCount(t, s, "coverage_samples") != 0 || resolverAuditCount(t, s) != 0 {
		t.Fatal("settings invented evidence or consent")
	}
}

func TestHTTPSConfigurationAtomicAuditAndImmutability(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	if _, err := s.conn.ExecContext(ctx, `CREATE TRIGGER reject_settings_audit BEFORE INSERT ON audit_events WHEN NEW.kind='https-configuration' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err == nil {
		t.Fatal("save without audit")
	}
	if settingsCount(t, s, "https_configurations") != 0 {
		t.Fatal("failed audit retained settings")
	}
	if _, err := s.conn.ExecContext(ctx, `DROP TRIGGER reject_settings_audit`); err != nil {
		t.Fatal(err)
	}
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	id := saved.Disclosure().Selection.ID
	for _, statement := range []string{
		`UPDATE https_configurations SET configuration='{}' WHERE id=?`,
		`UPDATE https_configurations SET endpoint_id='changed' WHERE id=?`,
		`UPDATE https_configurations SET scope_id='changed' WHERE id=?`,
		`DELETE FROM https_configurations WHERE id=?`,
		`INSERT OR REPLACE INTO https_configurations SELECT id, scope_id, endpoint_id, request_id, profile, '{}', created_at_ns, retired_at_ns, audit_expires_at_ns FROM https_configurations WHERE id=?`,
	} {
		if _, err := s.conn.ExecContext(ctx, statement, id); err == nil {
			t.Fatal("immutable record mutated")
		}
	}
	if _, err := s.conn.ExecContext(ctx, `CREATE TRIGGER reject_settings_retirement BEFORE INSERT ON audit_events WHEN NEW.kind='https-configuration' AND json_extract(NEW.payload,'$.state')='retired' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireHTTPSConfiguration(ctx, id); err == nil {
		t.Fatal("retired without audit")
	}
	if _, err := s.ActiveHTTPSConfiguration(ctx, id); err != nil {
		t.Fatal("failed retirement changed active settings")
	}
}

func TestHTTPSConfigurationRejectsInvalidAndRetiredScope(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	for _, change := range []func(*httpsplan.Configuration){
		func(c *httpsplan.Configuration) { c.Selection.ID = "old" },
		func(c *httpsplan.Configuration) { c.Selection.RequestID = "old" },
		func(c *httpsplan.Configuration) { c.Selection.EndpointID = "old" },
		func(c *httpsplan.Configuration) { c.Selection.Family = nq.FamilyIPv6 },
		func(c *httpsplan.Configuration) { c.Selection.Method = "POST" },
		func(c *httpsplan.Configuration) { c.Selection.ExpectedStatus = 0 },
		func(c *httpsplan.Configuration) { c.ServerName = "private.example." },
		func(c *httpsplan.Configuration) { c.ServerName = strings.Repeat("a", 5000) },
		func(c *httpsplan.Configuration) { c.Endpoint = netip.MustParseAddrPort("127.0.0.1:443") },
		func(c *httpsplan.Configuration) { c.Endpoint = netip.MustParseAddrPort("198.51.100.20:80") },
		func(c *httpsplan.Configuration) { c.DestinationPolicy = "" },
	} {
		c := httpsSettings()
		change(&c)
		if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", c); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	if _, err := s.CreateHTTPSConfiguration(ctx, "missing", httpsSettings()); err == nil {
		t.Fatal("missing enrollment accepted")
	}
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(ctx, `UPDATE network_scopes SET retired_at_ns=? WHERE id='scope.home'`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveHTTPSConfiguration(ctx, saved.Disclosure().Selection.ID); err == nil {
		t.Fatal("retired enrollment resolved")
	}
	if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err == nil {
		t.Fatal("retired enrollment accepted")
	}
}

func TestHTTPSConfigurationConcurrentAndLifetimeBounds(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { _, _ = s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()) })
	}
	wg.Wait()
	if got := settingsCount(t, s, "https_configurations"); got != 16 {
		t.Fatalf("concurrent active limit = %d", got)
	}
	// Retire in one statement to exercise per-row atomic audit triggers, then fill
	// the bounded archive through the public API (which cannot recycle IDs).
	if _, err := s.conn.ExecContext(ctx, `UPDATE https_configurations SET retired_at_ns=?`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	for i := 16; i < 256; i++ {
		saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RetireHTTPSConfiguration(ctx, saved.Disclosure().Selection.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err == nil {
		t.Fatal("lifetime bound exceeded")
	}
	if settingsCount(t, s, "https_configurations") != 256 {
		t.Fatal("archive not retained")
	}
}

func TestHTTPSConfigurationMigratesV1AndPreservesRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, migrationV1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO network_scopes VALUES ('scope.home','lan',1,NULL,'{}'); PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if version, err := s.SchemaVersion(ctx); err != nil || version != schemaVersion {
		t.Fatalf("migration: %d %v", version, err)
	}
	if settingsCount(t, s, "network_scopes") != 1 {
		t.Fatal("migration lost enrollment")
	}
	if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSConfigurationClockAndCancellation(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	now := time.Now().Round(0).UTC()
	s.now = func() time.Time { return now }
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Second)
	if err := s.RetireHTTPSConfiguration(ctx, saved.Disclosure().Selection.ID); err == nil {
		t.Fatal("retirement clock reversal accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.CreateHTTPSConfiguration(canceled, "scope.home", httpsSettings()); err == nil {
		t.Fatal("canceled save accepted")
	}
	if _, err := s.ActiveHTTPSConfiguration(canceled, saved.Disclosure().Selection.ID); err == nil {
		t.Fatal("canceled read accepted")
	}
	for _, invalid := range []time.Time{{}, time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		now = invalid
		if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err == nil {
			t.Fatal("invalid clock accepted")
		}
	}
}

func TestHTTPSConfigurationMigrationFailureIsAtomic(t *testing.T) {
	for _, sourceVersion := range []int{1, 2} {
		t.Run(fmt.Sprint(sourceVersion), func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			schema := migrationV1
			if sourceVersion == 2 {
				schema += migrationV2
			}
			if _, err = db.ExecContext(ctx, schema); err != nil {
				t.Fatal(err)
			}
			// Fail after V3 creates its table. When starting from V1, V2 must roll
			// back too; a half-upgraded store cannot be left behind.
			if _, err = db.ExecContext(ctx, `CREATE TRIGGER https_configuration_created AFTER INSERT ON audit_events BEGIN SELECT 1; END; PRAGMA user_version=`+fmt.Sprint(sourceVersion)); err != nil {
				t.Fatal(err)
			}
			if store, err := Open(dir, DefaultLimits()); err == nil {
				store.Close()
				t.Fatal("broken migration accepted")
			}
			var version, tables, resolvers int
			if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='https_configurations'`).Scan(&tables); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='resolver_configurations'`).Scan(&resolvers); err != nil {
				t.Fatal(err)
			}
			if version != sourceVersion || tables != 0 || resolvers != sourceVersion-1 {
				t.Fatalf("partial migration: version %d HTTPS %d resolver %d", version, tables, resolvers)
			}
		})
	}
}

func TestHTTPSMigrationPreservesExistingV2ResolverSettings(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, migrationV1+migrationV2); err != nil {
		t.Fatal(err)
	}
	old := resolverSettings()
	old.Selection.ID = "selection." + strings.Repeat("a", 32)
	old.Selection.ResolverID = "resolver." + strings.Repeat("b", 32)
	old.Selection.QueryID = "query." + strings.Repeat("c", 32)
	encoded, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO network_scopes VALUES ('scope.home','lan',1,NULL,'{}'); PRAGMA user_version=2`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO resolver_configurations(id,scope_id,resolver_id,query_id,configuration,created_at_ns,audit_expires_at_ns) VALUES (?,'scope.home',?,?,?,?,?)`, old.Selection.ID, old.Selection.ResolverID, old.Selection.QueryID, string(encoded), int64(2), time.Now().Add(time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(dir, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if version, err := s.SchemaVersion(ctx); err != nil || version != 3 {
		t.Fatalf("schema %d: %v", version, err)
	}
	retained, err := s.ActiveResolverConfiguration(ctx, old.Selection.ID)
	if err != nil || retained.Disclosure() != old {
		t.Fatal("migration changed existing resolver settings")
	}
	if settingsCount(t, s, "audit_events") != 1 {
		t.Fatal("migration rewrote resolver audits")
	}
	if _, err = s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err = s.conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal mode changed: %s %v", mode, err)
	}
}

func TestHTTPSSettingsReadsUseIndependentBoundedPool(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	id := saved.Disclosure().Selection.ID
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE network_scopes SET retired_at_ns=? WHERE id='scope.home'`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	// A reader on the pinned writer would incorrectly observe this uncommitted
	// retirement. The separate mode=ro pool sees the committed enrollment.
	if _, err = s.ActiveHTTPSConfiguration(ctx, id); err != nil {
		t.Fatal("read shared the writer transaction")
	}
	tx.Rollback()
	hold, err := s.gatewayHistoryDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	started := time.Now()
	if _, err = s.ActiveHTTPSConfiguration(ctx, id); err != ErrHTTPSConfiguration {
		t.Fatal("queued read was not bounded")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("read timeout exceeded: %v", elapsed)
	}
}

func TestHTTPSSettingsRejectCorruptRowsWithoutPartialList(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"duplicate keys": func(raw string) string {
			return strings.Replace(raw, `"ServerName":"private.example"`, `"ServerName":"private.example","ServerName":"private.example"`, 1)
		},
		"unknown field":      func(raw string) string { return strings.TrimSuffix(raw, "}") + `,"Extra":"unexpected"}` },
		"case alias":         func(raw string) string { return strings.Replace(raw, "private.example", "Private.Example", 1) },
		"reference mismatch": func(raw string) string { return strings.Replace(raw, `"ExpectedStatus":204`, `"ExpectedStatus":0`, 1) },
		"redacted string":    func(raw string) string { return `"[HTTPS configuration]"` },
		"encoded injection": func(raw string) string {
			return strings.Replace(raw, "/private-check?test=1", "/private-check%0d%0a", 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, _ := settingsStore(t)
			if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err != nil {
				t.Fatal(err)
			}
			saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
			if err != nil {
				t.Fatal(err)
			}
			id := saved.Disclosure().Selection.ID
			var raw string
			if err = s.conn.QueryRowContext(ctx, `SELECT configuration FROM https_configurations WHERE id=?`, id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			corrupted := edit(raw)
			if corrupted == raw {
				t.Fatal("fixture did not corrupt record")
			}
			if _, err = s.conn.ExecContext(ctx, `DROP TRIGGER https_configuration_update_guard; DROP TRIGGER https_configuration_retired`); err != nil {
				t.Fatal(err)
			}
			// The schema rejects non-object data itself. Other malformed but valid JSON
			// must be rejected by decoding, with no earlier valid row returned as success.
			_, err = s.conn.ExecContext(ctx, `UPDATE https_configurations SET configuration=? WHERE id=?`, corrupted, id)
			if name == "redacted string" {
				if err == nil {
					t.Fatal("stored redacted placeholder")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ActiveHTTPSConfiguration(ctx, id); err != ErrHTTPSConfiguration {
				t.Fatal("resolved corrupt settings")
			}
			if rows, err := s.ListActiveHTTPSConfigurations(ctx, "scope.home"); err != ErrHTTPSConfiguration || rows != nil {
				t.Fatal("returned partial settings list")
			}
		})
	}
}

func TestHTTPSSettingsValidateReferenceMetadataAndEnrollmentTime(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	cfg := saved.Disclosure()
	encoded, err := json.Marshal(httpsplan.DisclosedConfiguration(cfg))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, endpoint, request, profile string
		created, enrolled              int64
	}{
		{"https-selection.bad", cfg.Selection.EndpointID, cfg.Selection.RequestID, httpsplan.Profile, 2, 1},
		{cfg.Selection.ID, "https-endpoint." + strings.Repeat("f", 32), cfg.Selection.RequestID, httpsplan.Profile, 2, 1},
		{cfg.Selection.ID, cfg.Selection.EndpointID, "https-request.bad", httpsplan.Profile, 2, 1},
		{cfg.Selection.ID, cfg.Selection.EndpointID, cfg.Selection.RequestID, "selected-https-v2", 2, 1},
		{cfg.Selection.ID, cfg.Selection.EndpointID, cfg.Selection.RequestID, httpsplan.Profile, 1, 2},
	} {
		row := s.conn.QueryRowContext(ctx, `SELECT ?, 'scope.home', ?, ?, ?, ?, ?, ?`, tc.id, tc.created, string(encoded), tc.endpoint, tc.request, tc.profile, tc.enrolled)
		if _, err := scanHTTPSConfiguration(row); err != ErrHTTPSConfiguration {
			t.Fatal("accepted invalid reference/profile/time metadata")
		}
	}
	now := time.Now().Add(-time.Hour)
	s.now = func() time.Time { return now }
	if _, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings()); err != ErrHTTPSConfiguration {
		t.Fatal("created settings before enrollment")
	}
}

func TestHTTPSSettingsPreserveMaximumReviewedRequest(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	config := httpsSettings()
	config.ServerName = strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	config.RequestTarget = "/" + strings.Repeat("&", httpsplan.MaxRequestTargetBytes-1)
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", config)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.ActiveHTTPSConfiguration(ctx, saved.Disclosure().Selection.ID)
	if err != nil || loaded.Disclosure() != saved.Disclosure() {
		t.Fatal("maximum valid request did not round trip")
	}
}

func TestHTTPSAuditRetentionDoesNotEraseAttributionOrReviveSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	saved, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	original := saved.Disclosure()
	if err = s.RetireHTTPSConfiguration(ctx, original.Selection.ID); err != nil {
		t.Fatal(err)
	}
	var expires int64
	if err = s.conn.QueryRowContext(ctx, `SELECT max(expires_at_ns) FROM audit_events WHERE kind=?`, HTTPSConfigurationAuditKind).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PruneExpired(ctx, time.Unix(0, expires).Add(time.Second), 100); err != nil {
		t.Fatal(err)
	}
	if settingsCount(t, s, "https_configurations") != 1 {
		t.Fatal("audit pruning erased immutable attribution")
	}
	if _, err = s.ActiveHTTPSConfiguration(ctx, original.Selection.ID); err != ErrHTTPSConfiguration {
		t.Fatal("audit pruning revived settings")
	}
	next, err := s.CreateHTTPSConfiguration(ctx, "scope.home", httpsSettings())
	if err != nil {
		t.Fatal(err)
	}
	if next.Disclosure().Selection.ID == original.Selection.ID {
		t.Fatal("pruned audit permitted reference reuse")
	}
}
