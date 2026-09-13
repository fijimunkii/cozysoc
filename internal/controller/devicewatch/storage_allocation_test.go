package devicewatch

import (
	"context"
	"database/sql"
	"net/url"
	"testing"
	"time"
)

// Allocation contains only schema object names and byte/page totals. It never
// exports rows, addresses, observation payloads or household identifiers.
type storageAllocation struct {
	PageSize  int64                     `json:"page_size"`
	PageCount int64                     `json:"page_count"`
	FreePages int64                     `json:"free_pages"`
	Objects   []storageObjectAllocation `json:"objects"`
}

type storageObjectAllocation struct {
	Name         string `json:"name"`
	Table        string `json:"table"`
	Kind         string `json:"kind"`
	Pages        int64  `json:"pages"`
	Bytes        int64  `json:"bytes"`
	PayloadBytes int64  `json:"payload_bytes"`
	UnusedBytes  int64  `json:"unused_bytes"`
}

// Called only between completed collections, or after closing the writer. Use
// the production SQLite driver in read-only mode without changing journal,
// durability, vacuum or schema settings for the measurement.
func readStorageAllocation(t *testing.T, path string) storageAllocation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var result storageAllocation
	for _, item := range []struct {
		query string
		value *int64
	}{
		{"PRAGMA page_size", &result.PageSize},
		{"PRAGMA page_count", &result.PageCount},
		{"PRAGMA freelist_count", &result.FreePages},
	} {
		if err := tx.QueryRowContext(ctx, item.query).Scan(item.value); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.name, COALESCE(s.tbl_name, 'sqlite_schema'), COALESCE(s.type, 'table'), COUNT(*), SUM(d.pgsize), SUM(d.payload), SUM(d.unused)
 FROM dbstat AS d LEFT JOIN sqlite_schema AS s ON s.name=d.name
 GROUP BY d.name, s.tbl_name, s.type ORDER BY d.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var allocated int64
	for rows.Next() {
		var object storageObjectAllocation
		if err := rows.Scan(&object.Name, &object.Table, &object.Kind, &object.Pages, &object.Bytes, &object.PayloadBytes, &object.UnusedBytes); err != nil {
			t.Fatal(err)
		}
		if object.Pages <= 0 || object.Bytes != object.Pages*result.PageSize || object.PayloadBytes < 0 || object.UnusedBytes < 0 || object.PayloadBytes+object.UnusedBytes > object.Bytes {
			t.Fatalf("invalid object allocation: %+v", object)
		}
		allocated += object.Pages
		result.Objects = append(result.Objects, object)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if allocated+result.FreePages != result.PageCount {
		t.Fatalf("unaccounted database pages: allocated=%d free=%d total=%d", allocated, result.FreePages, result.PageCount)
	}
	return result
}

func TestStorageAllocationIncludesIndexesAndFreePages(t *testing.T) {
	path := t.TempDir() + "/allocation.db"
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE evidence(id INTEGER PRIMARY KEY, value BLOB UNIQUE);
 INSERT INTO evidence VALUES (1, zeroblob(65536));
 INSERT INTO evidence VALUES (2, zeroblob(32768));
 DELETE FROM evidence WHERE id=1;`); err != nil {
		t.Fatal(err)
	}
	allocation := readStorageAllocation(t, path)
	if allocation.FreePages == 0 {
		t.Fatal("fixture did not exercise free pages")
	}
	var table, index bool
	for _, object := range allocation.Objects {
		if object.Table == "evidence" && object.PayloadBytes > 0 {
			table = table || object.Kind == "table"
			index = index || object.Kind == "index"
		}
	}
	if !table || !index {
		t.Fatal("missing table or automatic unique-index allocation")
	}
}
