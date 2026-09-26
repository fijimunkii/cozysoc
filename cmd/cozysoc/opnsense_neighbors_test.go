package main

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/core"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/opnsense"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

func TestOPNsenseNeighborsUsesActiveScopeWithoutClaimingPresence(t *testing.T) {
	at := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	observations, _, err := opnsense.BuildObservations(opnsense.Snapshot{IPv4Total: 1, Neighbors: []opnsense.Neighbor{{IP: netip.MustParseAddr("192.168.1.8"), MAC: "02:00:00:00:00:08", Interface: "lan", Family: "ipv4"}}},
		"scope.home", "sensor.opnsense.fixture", []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, at)
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeDeviceStore{activeScopes: []domain.NetworkScope{{ID: "scope.home"}}, observations: storage.ObservationPage{Observations: observations}}
	handler, err := newControllerAPIHandler(core.New("test", 1, time.Second, nil), store, &fakeDeviceWatchAPIControl{})
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return at.Add(time.Minute) }
	got, err := handler.OPNsenseNeighbors(context.Background())
	if err != nil || !got.ScopeEnrolled || got.ScopeID != "scope.home" || len(got.Reports) != 1 ||
		got.Reports[0].Address != "192.168.1.8" || store.observationQuery.ScopeID != "scope.home" ||
		store.observationQuery.Kind != opnsense.NeighborObservationKind {
		t.Fatalf("router evidence was misprojected: %+v query=%+v err=%v", got, store.observationQuery, err)
	}
	store.activeScopes = nil
	got, err = handler.OPNsenseNeighbors(context.Background())
	if err != nil || got.ScopeEnrolled || len(got.Reports) != 0 {
		t.Fatalf("no enrolled scope: %+v, %v", got, err)
	}
}
