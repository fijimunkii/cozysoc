package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStorePreservesLiteralPaths(t *testing.T) {
	for _, name := range []string{
		"with spaces", "question?mark", "hash#mark", "percent%23mark",
		"all ?#% together", "options?mode=memory&cache=shared", "file:literal",
	} {
		for _, relative := range []bool{false, true} {
			kind := "absolute"
			if relative {
				kind = "relative"
			}
			t.Run(kind+"/"+name, func(t *testing.T) {
				root := t.TempDir()
				t.Chdir(root)
				dir := filepath.Join(root, name)
				if relative {
					dir = name
				}
				path := filepath.Join(root, name, Filename)
				ctx := context.Background()
				s, err := Open(dir, DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = s.Close() })
				_, q, events := seedHistoryStore(t, s)
				insertHistory(t, s, events)
				before, err := s.ReadGatewayHistory(ctx, q)
				if err != nil || len(before.Runs) != 1 || !before.Runs[0].TerminalRetained {
					t.Fatalf("read written history: %+v, %v", before, err)
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = Open(dir, DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				s.now = func() time.Time { return q.AsOf }
				after, err := s.ReadGatewayHistory(ctx, q)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("reopen changed history: %+v, %v", after, err)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("literal database permissions: %v, %v", info, err)
				}
				if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
					t.Fatalf("state directory permissions: %v, %v", info, err)
				}
				for _, row := range []*sql.Row{
					s.conn.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`),
					s.gatewayHistoryDB.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`),
				} {
					var actual string
					if err := row.Scan(&actual); err != nil {
						t.Fatal(err)
					}
					opened, err := os.Stat(actual)
					if err != nil || !os.SameFile(info, opened) {
						t.Fatalf("connection opened another database %q: %v", actual, err)
					}
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				var files []string
				if err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
					if err == nil && !entry.IsDir() {
						files = append(files, p)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(files, []string{path}) {
					t.Fatalf("unexpected database or sidecar files: %q", files)
				}
			})
		}
	}
}
