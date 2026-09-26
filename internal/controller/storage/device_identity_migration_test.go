package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDeviceIdentitySchemaV5MigratesExistingEvidenceAndRetriesAtomically(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "rollback"}[conflict], func(t *testing.T) {
			dir := t.TempDir()
			dsn, err := sqliteFileURI(filepath.Join(dir, Filename))
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(context.Background(), migrationV1+migrationV2+migrationV3+migrationV4+`
				INSERT INTO network_scopes VALUES('scope.home','lan',1,NULL,'{}');
				INSERT INTO sensors VALUES('sensor.home','scope.home','device-watch','builtin',1,'{}');
				INSERT INTO devices VALUES('device.existing',NULL,1,NULL);
				PRAGMA user_version=4;`); err != nil {
				t.Fatal(err)
			}
			if conflict {
				if _, err := db.ExecContext(context.Background(), `CREATE TABLE device_identity_merges(dummy INTEGER) STRICT`); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if conflict {
				if s, err := Open(dir, DefaultLimits()); err == nil {
					s.Close()
					t.Fatal("conflicting v5 object accepted")
				}
				db, err = sql.Open("sqlite", dsn)
				if err != nil {
					t.Fatal(err)
				}
				var version, existing, partial int
				if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`SELECT count(*) FROM devices WHERE id='device.existing'`).Scan(&existing); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='device_identity_merges_target'`).Scan(&partial); err != nil {
					t.Fatal(err)
				}
				if version != 4 || existing != 1 || partial != 0 {
					t.Fatal("failed migration changed v4", version, existing, partial)
				}
				if _, err := db.Exec(`DROP TABLE device_identity_merges`); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			s, err := Open(dir, DefaultLimits())
			if err != nil {
				t.Fatal("v5 migration", err)
			}
			defer s.Close()
			if version, err := s.SchemaVersion(context.Background()); err != nil || version != 5 {
				t.Fatal("schema version", version, err)
			}
			var count int
			if err := s.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM devices WHERE id='device.existing'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("v4 device changed", count, err)
			}
			if err := s.conn.QueryRowContext(context.Background(), `SELECT count(*) FROM device_identity_merges`).Scan(&count); err != nil || count != 0 {
				t.Fatal("merge table unavailable", count, err)
			}
		})
	}
}
