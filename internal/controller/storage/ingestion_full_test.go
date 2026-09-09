package storage

import (
	"context"
	"errors"
	"testing"

	sqlite3 "modernc.org/sqlite/lib"
)

type codedIngestionError struct {
	code int
}

func (e codedIngestionError) Error() string { return "coded ingestion failure" }
func (e codedIngestionError) Code() int     { return e.code }

func TestClassifyIngestionFailureUsesSQLiteResultCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "generic", err: errors.New("write failed"), want: IngestionFailureWriteFailed},
		{name: "sqlite full", err: codedIngestionError{code: sqlite3.SQLITE_FULL}, want: IngestionFailureSQLiteFull},
		{name: "wrapped sqlite full", err: errors.Join(errors.New("outer"), codedIngestionError{code: sqlite3.SQLITE_FULL}), want: IngestionFailureSQLiteFull},
		{name: "extended sqlite full", err: codedIngestionError{code: sqlite3.SQLITE_FULL | (3 << 8)}, want: IngestionFailureSQLiteFull},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyIngestionFailure(test.err); got != test.want {
				t.Fatalf("failure class = %q, want %q", got, test.want)
			}
		})
	}
}

func TestIngestionSQLiteFullClearsOnlyAfterSuccessfulWrite(t *testing.T) {
	ingestor, err := newIngestor(&fakeIngestionSink{}, 4, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ingestor.Close(context.Background()) }()

	ingestor.noteFailure(IngestionCoverage, codedIngestionError{code: sqlite3.SQLITE_FULL})
	failed := ingestor.Health()
	if failed.State != IngestionHealthStorageFull || failed.FailureClass != IngestionFailureSQLiteFull || failed.Failed != 1 {
		t.Fatalf("sqlite full health = %+v", failed)
	}

	ingestor.noteProcessed(IngestionResult{Kind: IngestionCoverage, Inserted: true})
	recovered := ingestor.Health()
	if recovered.State != IngestionHealthCurrent || recovered.FailureClass != "" || recovered.Processed != 1 || recovered.Failed != 1 {
		t.Fatalf("recovered ingestion health = %+v", recovered)
	}
}
