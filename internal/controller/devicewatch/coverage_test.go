package devicewatch

import (
	"context"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/capability"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type fakeCoverageReader struct {
	sample domain.CoverageSample
	ok     bool
	err    error
}

func (f fakeCoverageReader) LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error) {
	return f.sample, f.ok, f.err
}

func TestCoverageVerificationSignalStates(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	tests := []struct {
		name   string
		reader fakeCoverageReader
		want   capability.VerificationSignalStatus
	}{
		{name: "missing", reader: fakeCoverageReader{}, want: capability.SignalMissing},
		{name: "fresh partial", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Minute), "partial")}, want: capability.SignalFresh},
		{name: "fresh unavailable", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Minute), "unavailable")}, want: capability.SignalFailed},
		{name: "stale historical replay", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Hour), "partial")}, want: capability.SignalStale},
		{name: "future clock skew", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(2*time.Minute), "partial")}, want: capability.SignalFailed},
		{name: "unknown status fails closed", reader: fakeCoverageReader{ok: true, sample: coverageFixture(now.Add(-time.Minute), "mystery")}, want: capability.SignalFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			signal, err := coverageVerificationSignal(context.Background(), test.reader, "scope.home", now)
			if err != nil {
				t.Fatal(err)
			}
			if signal.ID != "observation-freshness" || signal.Status != test.want {
				t.Fatalf("coverage signal = %+v, want status %s", signal, test.want)
			}
		})
	}
}

func TestFreshCoverageDoesNotRequireObservedNeighbors(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	sample := coverageFixture(now.Add(-time.Minute), "partial")
	sample.Evidence = []byte(`{"schema_version":1,"neighbors_in_scope":0,"whole_network_traffic_visible":false}`)
	signal, err := coverageVerificationSignal(context.Background(), fakeCoverageReader{sample: sample, ok: true}, "scope.home", now)
	if err != nil {
		t.Fatal(err)
	}
	if signal.Status != capability.SignalFresh {
		t.Fatalf("quiet but current coverage = %+v", signal)
	}
}

func coverageFixture(endedAt time.Time, status string) domain.CoverageSample {
	return domain.CoverageSample{
		ID: "coverage.test", ScopeID: "scope.home", SensorID: "sensor.dw.test", CapabilityID: CapabilityID,
		Status: status, StartedAt: endedAt, EndedAt: endedAt, SchemaVersion: 1,
		Evidence: []byte(`{"schema_version":1}`), Retention: domain.RetentionShort,
	}
}
