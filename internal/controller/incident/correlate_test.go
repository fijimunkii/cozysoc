package incident

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

func signal(id, category, severity, family string, at time.Time, confidence *float64) Signal {
	return Signal{Finding: domain.Finding{ID: id, ScopeID: "scope.home", DetectorID: "detector." + family,
		DetectorVersion: "1", Category: category, Severity: severity, Confidence: confidence,
		ObservedAt: at, CreatedAt: at.Add(time.Second), SchemaVersion: 1,
		Payload: []byte(`{"schema_version":1}`), EvidenceObservationIDs: []string{"obs." + id}, Retention: domain.RetentionStandard},
		DeviceID: "device.lamp", IdentityAuthority: domain.LinkUser, SourceFamily: family}
}

func confidence(value float64) *float64 { return &value }

func TestCorrelateSameConfirmedDeviceWithoutInflatingConfidence(t *testing.T) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	first := signal("finding.new", "new-device", "low", "device-watch", base.Add(time.Minute), confidence(.4))
	second := signal("finding.connection", "suspicious-connection", "high", "traffic", base.Add(7*time.Minute), confidence(.7))
	third := signal("finding.tripwire", "tripwire", "medium", "traffic", base.Add(11*time.Minute), confidence(.5))
	inputs := []Signal{third, first, second, second}
	got, err := Correlate(inputs)
	if err != nil || len(got) != 1 {
		t.Fatalf("correlation: %+v, %v", got, err)
	}
	item := got[0]
	if item.DeviceID != "device.lamp" || item.Severity != "high" || item.Confidence == nil || *item.Confidence != .7 ||
		item.CorrelationReason != "same-user-confirmed-device-and-utc-window" || len(item.FindingIDs) != 3 ||
		!reflect.DeepEqual(item.SourceFamilies, []string{"device-watch", "traffic"}) || len(item.EvidenceObservationIDs) != 3 {
		t.Fatalf("overstated or incomplete incident: %+v", item)
	}
	initial, err := Correlate([]Signal{first})
	if err != nil || len(initial) != 1 || initial[0].ID != item.ID {
		t.Fatalf("late findings changed incident identity: %+v, %v", initial, err)
	}
	slices.Reverse(inputs)
	reversed, err := Correlate(inputs)
	if err != nil || !reflect.DeepEqual(got, reversed) {
		t.Fatalf("out-of-order replay changed incident: %+v, %v", reversed, err)
	}
}

func TestUncertainIdentityAndWindowBoundaryDoNotMerge(t *testing.T) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	first := signal("finding.one", "new-device", "low", "device-watch", base.Add(14*time.Minute), nil)
	second := signal("finding.two", "suspicious-connection", "high", "traffic", base.Add(16*time.Minute), confidence(.8))
	got, err := Correlate([]Signal{first, second})
	if err != nil || len(got) != 2 || got[0].ID == got[1].ID || !got[0].WindowStart.Before(got[1].WindowStart) {
		t.Fatalf("boundary merged unrelated times: %+v, %v", got, err)
	}
	second.Finding.ObservedAt = base.Add(2 * time.Minute)
	second.Finding.CreatedAt = second.Finding.ObservedAt.Add(time.Second)
	second.IdentityAuthority = domain.LinkInferred
	got, err = Correlate([]Signal{first, second})
	if err != nil || len(got) != 2 {
		t.Fatalf("inferred device merged or was represented as confirmed: %+v, %v", got, err)
	}
	for _, item := range got {
		if len(item.FindingIDs) == 1 && item.FindingIDs[0] == second.Finding.ID && item.DeviceID != "" {
			t.Fatalf("inferred identity represented as confirmed: %+v", item)
		}
	}
	second.IdentityAuthority = domain.LinkUser
	second.Finding.Category = "unrelated-category"
	got, err = Correlate([]Signal{first, second})
	if err != nil || len(got) != 2 {
		t.Fatalf("unlisted category merged: %+v, %v", got, err)
	}
}

func TestUnknownConfidenceAndConflictingReplay(t *testing.T) {
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	first := signal("finding.one", "new-device", "low", "device-watch", base.Add(time.Minute), nil)
	second := signal("finding.two", "tripwire", "critical", "tripwire", base.Add(2*time.Minute), confidence(.9))
	got, err := Correlate([]Signal{first, second})
	if err != nil || len(got) != 1 || got[0].Confidence != nil {
		t.Fatalf("unknown confidence was promoted: %+v, %v", got, err)
	}
	conflict := second
	conflict.Finding.Payload = []byte(`{"schema_version":1,"changed":true}`)
	if _, err := Correlate([]Signal{second, conflict}); !errors.Is(err, ErrInput) {
		t.Fatalf("conflicting replay replaced evidence: %v", err)
	}
	bad := first
	bad.DeviceID = "device\nunsafe"
	if _, err := Correlate([]Signal{bad}); !errors.Is(err, ErrInput) {
		t.Fatalf("invalid identity accepted: %v", err)
	}
}
