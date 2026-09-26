package opnsense

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type historyReader struct {
	query storage.ObservationQuery
	page  storage.ObservationPage
}

func (reader *historyReader) ListObservations(_ context.Context, query storage.ObservationQuery) (storage.ObservationPage, error) {
	reader.query = query
	return reader.page, nil
}

func TestRecentNeighborsRetainsSourceAndScopeWithoutIdentityClaims(t *testing.T) {
	at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	observations, _, err := BuildObservations(Snapshot{IPv4Total: 1, Neighbors: []Neighbor{{IP: netip.MustParseAddr("192.168.1.8"), MAC: "02:00:00:00:00:08", Interface: "lan", Family: "ipv4"}}},
		"scope.home", "sensor.opnsense.fixture", []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, at)
	if err != nil || len(observations) != 1 {
		t.Fatalf("fixture: %+v, %v", observations, err)
	}
	reader := &historyReader{page: storage.ObservationPage{Observations: observations, Next: &storage.ObservationCursor{ID: "obs.older", IngestedAt: at.Add(-time.Second)}}}
	history, err := RecentNeighbors(context.Background(), reader, "scope.home", at.Add(time.Minute))
	if err != nil || len(history.Reports) != 1 || !history.Truncated || history.ScopeID != "scope.home" ||
		history.Reports[0].Address != "192.168.1.8" || history.Reports[0].Hardware != "02:00:00:00:00:08" ||
		history.Reports[0].CapturedAt != at || reader.query.Kind != NeighborObservationKind ||
		reader.query.ScopeID != "scope.home" || reader.query.Limit != MaxNeighborHistory {
		t.Fatalf("incorrect router history: %+v, query=%+v, err=%v", history, reader.query, err)
	}
	if reader.query.Since != at.Add(time.Minute-24*time.Hour) {
		t.Fatalf("unbounded history window: %+v", reader.query)
	}
	for name, mutate := range map[string]func(*domain.Observation){
		"wrong scope":  func(value *domain.Observation) { value.ScopeID = "scope.other" },
		"wrong source": func(value *domain.Observation) { value.SourceStream = "other" },
		"unknown family": func(value *domain.Observation) {
			value.Payload = []byte(`{"schema_version":1,"address":"192.168.1.8","hardware_address":"02:00:00:00:00:08","interface":"lan","family":"unknown"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			copyValue := observations[0]
			mutate(&copyValue)
			reader.page = storage.ObservationPage{Observations: []domain.Observation{copyValue}}
			if _, err := RecentNeighbors(context.Background(), reader, "scope.home", at.Add(time.Minute)); !errors.Is(err, ErrNeighborHistory) {
				t.Fatalf("invalid report accepted: %v", err)
			}
		})
	}
}
