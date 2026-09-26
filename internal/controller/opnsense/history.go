package opnsense

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const MaxNeighborHistory = 100

var ErrNeighborHistory = errors.New("OPNsense neighbor history is unavailable")

type NeighborReport struct {
	ObservationID string
	CapturedAt    time.Time
	Address       string
	Hardware      string
	Interface     string
	Family        string
}

type NeighborHistory struct {
	ScopeID   string
	AsOf      time.Time
	Reports   []NeighborReport
	Truncated bool
}

type neighborHistoryReader interface {
	ListObservations(context.Context, storage.ObservationQuery) (storage.ObservationPage, error)
}

// RecentNeighbors projects retained router reports as source evidence, never
// as current presence, a verified device identity, or monitoring coverage.
func RecentNeighbors(ctx context.Context, reader neighborHistoryReader, scopeID string, asOf time.Time) (NeighborHistory, error) {
	if reader == nil || asOf.IsZero() {
		return NeighborHistory{}, ErrNeighborHistory
	}
	asOf = asOf.UTC()
	page, err := reader.ListObservations(ctx, storage.ObservationQuery{ScopeID: scopeID,
		Kind: NeighborObservationKind, Since: asOf.Add(-24 * time.Hour), Until: asOf, Limit: MaxNeighborHistory})
	if err != nil {
		return NeighborHistory{}, ErrNeighborHistory
	}
	result := NeighborHistory{ScopeID: scopeID, AsOf: asOf, Reports: make([]NeighborReport, 0, len(page.Observations)), Truncated: page.Next != nil}
	for _, observation := range page.Observations {
		report, err := projectNeighborReport(observation, scopeID, asOf)
		if err != nil {
			return NeighborHistory{}, ErrNeighborHistory
		}
		result.Reports = append(result.Reports, report)
	}
	return result, nil
}

func projectNeighborReport(observation domain.Observation, scopeID string, asOf time.Time) (NeighborReport, error) {
	if observation.ScopeID != scopeID || observation.Kind != NeighborObservationKind ||
		!strings.HasPrefix(observation.SensorID, "sensor.opnsense.") ||
		observation.SourceStream != "opnsense-neighbors-v1" || observation.SourceTime == nil ||
		observation.SourceTime.After(asOf) || observation.IngestedAt.After(asOf) ||
		observation.SchemaVersion != 1 || observation.Retention != domain.RetentionEphemeral ||
		observation.Attribution != "opnsense:neighbor-table;device-identity-unverified" {
		return NeighborReport{}, ErrNeighborHistory
	}
	var payload struct {
		SchemaVersion int    `json:"schema_version"`
		Address       string `json:"address"`
		Hardware      string `json:"hardware_address"`
		Interface     string `json:"interface"`
		Family        string `json:"family"`
	}
	if len(observation.Payload) > 512 || json.Unmarshal(observation.Payload, &payload) != nil || payload.SchemaVersion != 1 {
		return NeighborReport{}, ErrNeighborHistory
	}
	ip, ipErr := netip.ParseAddr(payload.Address)
	mac, macErr := net.ParseMAC(payload.Hardware)
	if ipErr != nil || !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.Is4In6() || ip.Zone() != "" ||
		(payload.Family == "ipv4") != ip.Is4() || (payload.Family != "ipv4" && payload.Family != "ipv6") ||
		macErr != nil || len(mac) != 6 || mac[0]&1 != 0 || (mac[0]|mac[1]|mac[2]|mac[3]|mac[4]|mac[5]) == 0 ||
		!interfaceName.MatchString(payload.Interface) {
		return NeighborReport{}, ErrNeighborHistory
	}
	return NeighborReport{ObservationID: observation.ID, CapturedAt: observation.SourceTime.UTC(),
		Address: ip.String(), Hardware: mac.String(), Interface: payload.Interface, Family: payload.Family}, nil
}
