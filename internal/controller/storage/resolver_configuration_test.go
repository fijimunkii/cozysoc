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

	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverplan"
)

func resolverSettings() resolverplan.Configuration {
	return resolverplan.Configuration{Selection: nq.ResolverSelection{Family: nq.FamilyIPv4, Transport: nq.DNSUDP, QueryType: nq.DNSQueryA, Expect: nq.DNSExpectAnswer}, Endpoint: netip.MustParseAddrPort("192.0.2.53:53"), Name: "Private.Example.", DestinationScope: resolverplan.EnrolledPrefix}
}
func settingsStore(t *testing.T) (*Store, string) {
	t.Helper()
	s, dir := gatewayAuditStore(t)
	seedScopeAndSensor(t, s, "scope.home", "sensor.home", time.Now().UTC())
	return s, dir
}
func settingsCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	// Only test-owned constant table names enter this helper.
	if err := s.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestResolverConfigurationRestartRetirementAndRedaction(t *testing.T) {
	ctx := context.Background()
	s, dir := settingsStore(t)
	saved, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings())
	if err != nil {
		t.Fatal(err)
	}
	original := saved.Disclosure()
	if original.Name != "private.example." || original.Selection.ID == "" || original.Selection.QueryID == "" || original.Selection.ResolverID == "" {
		t.Fatalf("invalid disclosure: %v", saved)
	}
	copy := saved.Disclosure()
	copy.Name = "changed.example."
	if saved.Disclosure() != original {
		t.Fatal("disclosure aliases record")
	}
	encoded, _ := json.Marshal(saved)
	for _, value := range []string{string(encoded), fmt.Sprintf("%v %+v %#v", saved, saved, saved)} {
		if strings.Contains(value, "private.example") || strings.Contains(value, "192.0.2.53") {
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
	listed, err := s.ListActiveResolverConfigurations(ctx, "scope.home")
	if err != nil || len(listed) != 1 || listed[0].Disclosure() != original {
		t.Fatal("restart list lost settings")
	}
	loaded, err := s.ActiveResolverConfiguration(ctx, original.Selection.ID)
	if err != nil || loaded.Disclosure() != original || loaded.ScopeID != "scope.home" || !loaded.CreatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("restart changed settings: %v", err)
	}
	if err := s.RetireResolverConfiguration(ctx, original.Selection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveResolverConfiguration(ctx, original.Selection.ID); err == nil {
		t.Fatal("retired record resolved")
	}
	if err := s.RetireResolverConfiguration(ctx, original.Selection.ID); err == nil {
		t.Fatal("retirement replay succeeded")
	}
	listed, err = s.ListActiveResolverConfigurations(ctx, "scope.home")
	if err != nil || len(listed) != 0 {
		t.Fatal("list retained retired settings")
	}
	next := resolverSettings()
	next.Name = "other.example."
	next.Endpoint = netip.MustParseAddrPort("192.0.2.54:53")
	replacement, err := s.CreateResolverConfiguration(ctx, "scope.home", next)
	if err != nil {
		t.Fatal(err)
	}
	refs := replacement.Disclosure().Selection
	if refs.ID == original.Selection.ID || refs.ResolverID == original.Selection.ResolverID || refs.QueryID == original.Selection.QueryID {
		t.Fatal("replacement reused references")
	}
	var raw string
	if err := s.conn.QueryRowContext(ctx, `SELECT configuration FROM resolver_configurations WHERE id=?`, original.Selection.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var retained resolverplan.Configuration
	if json.Unmarshal([]byte(raw), &retained) != nil || retained != original {
		t.Fatal("old attribution changed")
	}
	rows, err := s.conn.QueryContext(ctx, `SELECT payload FROM audit_events WHERE kind=?`, ResolverConfigurationAuditKind)
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
		if strings.Contains(payload, "example") || strings.Contains(payload, "192.0.2") {
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

func TestResolverConfigurationAtomicAuditAndImmutability(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	if _, err := s.conn.ExecContext(ctx, `CREATE TRIGGER reject_settings_audit BEFORE INSERT ON audit_events WHEN NEW.kind='resolver-configuration' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings()); err == nil {
		t.Fatal("save without audit")
	}
	if settingsCount(t, s, "resolver_configurations") != 0 {
		t.Fatal("failed audit retained settings")
	}
	if _, err := s.conn.ExecContext(ctx, `DROP TRIGGER reject_settings_audit`); err != nil {
		t.Fatal(err)
	}
	saved, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings())
	if err != nil {
		t.Fatal(err)
	}
	id := saved.Disclosure().Selection.ID
	for _, statement := range []string{
		`UPDATE resolver_configurations SET configuration='{}' WHERE id=?`,
		`UPDATE resolver_configurations SET resolver_id='changed' WHERE id=?`,
		`UPDATE resolver_configurations SET scope_id='changed' WHERE id=?`,
		`DELETE FROM resolver_configurations WHERE id=?`,
		`INSERT OR REPLACE INTO resolver_configurations SELECT id, scope_id, resolver_id, query_id, '{}', created_at_ns, retired_at_ns, audit_expires_at_ns FROM resolver_configurations WHERE id=?`,
	} {
		if _, err := s.conn.ExecContext(ctx, statement, id); err == nil {
			t.Fatal("immutable record mutated")
		}
	}
	if _, err := s.conn.ExecContext(ctx, `CREATE TRIGGER reject_settings_retirement BEFORE INSERT ON audit_events WHEN NEW.kind='resolver-configuration' AND json_extract(NEW.payload,'$.state')='retired' BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireResolverConfiguration(ctx, id); err == nil {
		t.Fatal("retired without audit")
	}
	if _, err := s.ActiveResolverConfiguration(ctx, id); err != nil {
		t.Fatal("failed retirement changed active settings")
	}
}

func TestResolverConfigurationRejectsInvalidAndRetiredScope(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	for _, change := range []func(*resolverplan.Configuration){
		func(c *resolverplan.Configuration) { c.Selection.ID = "old" },
		func(c *resolverplan.Configuration) { c.Selection.QueryID = "old" },
		func(c *resolverplan.Configuration) { c.Selection.ResolverID = "old" },
		func(c *resolverplan.Configuration) { c.Selection.Family = nq.FamilyIPv6 },
		func(c *resolverplan.Configuration) { c.Selection.Transport = nq.DNSTCP },
		func(c *resolverplan.Configuration) { c.Selection.Expect = "" },
		func(c *resolverplan.Configuration) { c.Name = "private.example" },
		func(c *resolverplan.Configuration) { c.Name = strings.Repeat("a", 5000) },
		func(c *resolverplan.Configuration) { c.Endpoint = netip.MustParseAddrPort("127.0.0.1:53") },
		func(c *resolverplan.Configuration) { c.Endpoint = netip.MustParseAddrPort("192.0.2.53:54") },
		func(c *resolverplan.Configuration) { c.DestinationScope = "" },
	} {
		c := resolverSettings()
		change(&c)
		if _, err := s.CreateResolverConfiguration(ctx, "scope.home", c); err == nil {
			t.Fatal("invalid settings accepted")
		}
	}
	if _, err := s.CreateResolverConfiguration(ctx, "missing", resolverSettings()); err == nil {
		t.Fatal("missing enrollment accepted")
	}
	saved, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(ctx, `UPDATE network_scopes SET retired_at_ns=? WHERE id='scope.home'`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActiveResolverConfiguration(ctx, saved.Disclosure().Selection.ID); err == nil {
		t.Fatal("retired enrollment resolved")
	}
	if _, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings()); err == nil {
		t.Fatal("retired enrollment accepted")
	}
}

func TestResolverConfigurationConcurrentAndLifetimeBounds(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() { _, _ = s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings()) })
	}
	wg.Wait()
	if got := settingsCount(t, s, "resolver_configurations"); got != 16 {
		t.Fatalf("concurrent active limit = %d", got)
	}
	// Retire in one statement to exercise per-row atomic audit triggers, then fill
	// the bounded archive through the public API (which cannot recycle IDs).
	if _, err := s.conn.ExecContext(ctx, `UPDATE resolver_configurations SET retired_at_ns=?`, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	for i := 16; i < 256; i++ {
		saved, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings())
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RetireResolverConfiguration(ctx, saved.Disclosure().Selection.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings()); err == nil {
		t.Fatal("lifetime bound exceeded")
	}
	if settingsCount(t, s, "resolver_configurations") != 256 {
		t.Fatal("archive not retained")
	}
}

func TestResolverConfigurationMigratesV1AndPreservesRows(t *testing.T) {
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
	if _, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings()); err != nil {
		t.Fatal(err)
	}
}

func TestResolverConfigurationClockAndCancellation(t *testing.T) {
	ctx := context.Background()
	s, _ := settingsStore(t)
	now := time.Now().Round(0).UTC()
	s.now = func() time.Time { return now }
	saved, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Second)
	if err := s.RetireResolverConfiguration(ctx, saved.Disclosure().Selection.ID); err == nil {
		t.Fatal("retirement clock reversal accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.CreateResolverConfiguration(canceled, "scope.home", resolverSettings()); err == nil {
		t.Fatal("canceled save accepted")
	}
	if _, err := s.ActiveResolverConfiguration(canceled, saved.Disclosure().Selection.ID); err == nil {
		t.Fatal("canceled read accepted")
	}
	for _, invalid := range []time.Time{{}, time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		now = invalid
		if _, err := s.CreateResolverConfiguration(ctx, "scope.home", resolverSettings()); err == nil {
			t.Fatal("invalid clock accepted")
		}
	}
}

func TestResolverConfigurationMigrationFailureIsAtomic(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, migrationV1); err != nil {
		t.Fatal(err)
	}
	// A conflicting preexisting trigger fails after V2's CREATE TABLE. The entire
	// migration must roll back, retaining V1 and leaving no partial new table.
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER resolver_configuration_created AFTER INSERT ON audit_events BEGIN SELECT 1; END; PRAGMA user_version=1`); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(dir, DefaultLimits()); err == nil {
		store.Close()
		t.Fatal("broken migration accepted")
	}
	var version, tables int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='resolver_configurations'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 1 || tables != 0 {
		t.Fatalf("partial migration: version %d, tables %d", version, tables)
	}
}
