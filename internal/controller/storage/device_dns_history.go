package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

// DNS history is a bounded, private projection of the most recent retained
// resolver observations. It is never used as identity or coverage evidence.
const MaxDeviceDNSHistory = 100
const dnsIdentityWindow = 10 * time.Minute
const maxAdGuardHistorySensors = 16

type DeviceDNSHistoryItem struct {
	ObservedAt time.Time
	ClientIP   string
	Name       string
	QueryType  string
	Filtering  string
}

type DeviceDNSHistory struct {
	Items     []DeviceDNSHistoryItem
	Truncated bool
}

type DeviceDetailSnapshot struct {
	Detail     DeviceEvidenceDetail
	DNSHistory DeviceDNSHistory
}

type dnsObservationPayload struct {
	SchemaVersion int    `json:"schema_version"`
	Name          string `json:"name"`
	QueryType     string `json:"query_type"`
	ClientIP      string `json:"client_ip"`
	Filtering     string `json:"filtering"`
}

func (v *CanonicalEvidenceView) GetDeviceDetailSnapshot(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceDetailSnapshot, error) {
	return withCanonicalEvidenceSnapshot(ctx, v, func(snapshot *MixedIdentitySnapshot) (DeviceDetailSnapshot, error) {
		return snapshot.GetDeviceDetailSnapshot(ctx, query)
	})
}

func (s *Store) GetDeviceDetailSnapshot(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceDetailSnapshot, error) {
	view, err := NewCanonicalEvidenceView(s)
	if err != nil {
		return DeviceDetailSnapshot{}, err
	}
	return view.GetDeviceDetailSnapshot(ctx, query)
}

func (s *MixedIdentitySnapshot) GetDeviceDetailSnapshot(ctx context.Context, query DeviceEvidenceDetailQuery) (DeviceDetailSnapshot, error) {
	if s == nil || s.legacy == nil {
		return DeviceDetailSnapshot{}, ErrEvidenceBatchData
	}
	now := s.legacy.now
	if query.AsOf.IsZero() {
		query.AsOf = now
	}
	query.AsOf = query.AsOf.UTC()
	if query.AsOf.After(now) || !batchTimeFits(query.AsOf) {
		return DeviceDetailSnapshot{}, ErrEvidenceBatchData
	}
	detail, err := s.GetDeviceEvidenceDetail(ctx, query)
	if err != nil {
		return DeviceDetailSnapshot{}, err
	}
	history, err := s.listDeviceDNSHistory(ctx, query, detail)
	if err != nil {
		return DeviceDetailSnapshot{}, err
	}
	return DeviceDetailSnapshot{Detail: detail, DNSHistory: history}, nil
}

func (s *MixedIdentitySnapshot) listDeviceDNSHistory(ctx context.Context, query DeviceEvidenceDetailQuery, detail DeviceEvidenceDetail) (DeviceDNSHistory, error) {
	result := DeviceDNSHistory{Items: []DeviceDNSHistoryItem{}}
	// A truncated identity page cannot prove that the newest relevant claim is
	// present. Withhold attribution rather than treating a partial page as full.
	if detail.Truncated {
		return result, nil
	}
	observations, truncated, err := s.listScopedDNSObservations(ctx, query.ScopeID, query.AsOf)
	if err != nil {
		return DeviceDNSHistory{}, err
	}
	result.Truncated = truncated
	for _, observation := range observations {
		if observation.SourceStream != "adguard-querylog-v1" || observation.Attribution != "resolver-client-ip;device-identity-unverified" || observation.SourceTime == nil || observation.SourceTime.After(query.AsOf) {
			return DeviceDNSHistory{}, fmt.Errorf("invalid retained DNS observation provenance")
		}
		var payload dnsObservationPayload
		if err := json.Unmarshal(observation.Payload, &payload); err != nil || payload.SchemaVersion != 1 || !validDNSHistoryPayload(payload) {
			return DeviceDNSHistory{}, fmt.Errorf("invalid retained DNS observation payload")
		}
		at := observation.SourceTime.UTC()
		if at.Before(query.AsOf.Add(-24 * time.Hour)) {
			continue
		}
		if !matchesDeviceDNSClaim(detail.Evidence, payload.ClientIP, at) {
			continue
		}
		ip, _ := netip.ParseAddr(payload.ClientIP)
		kind := domain.ClaimIPv6
		if ip.Is4() {
			kind = domain.ClaimIPv4
		}
		// Search the full bounded retention window for competing IP claims.
		// The target still needs its own fresh Device Watch evidence above.
		candidates, err := s.FindRecentDevicesByClaim(ctx, query.ScopeID, kind, payload.ClientIP, at.Add(-MaxQueryWindow), at)
		if err != nil {
			return DeviceDNSHistory{}, err
		}
		if len(candidates) != 1 || candidates[0].ID != query.DeviceID {
			continue
		}
		result.Items = append(result.Items, DeviceDNSHistoryItem{ObservedAt: at, ClientIP: ip.String(), Name: payload.Name, QueryType: payload.QueryType, Filtering: payload.Filtering})
	}
	sort.SliceStable(result.Items, func(i, j int) bool { return result.Items[i].ObservedAt.After(result.Items[j].ObservedAt) })
	return result, nil
}

func (s *MixedIdentitySnapshot) listScopedDNSObservations(ctx context.Context, scopeID string, asOf time.Time) ([]domain.Observation, bool, error) {
	rows, err := s.legacy.tx.QueryContext(ctx, `SELECT id FROM sensors WHERE scope_id=? AND kind='adguard-home' AND ownership='external' ORDER BY id ASC LIMIT ?`, scopeID, maxAdGuardHistorySensors+1)
	if err != nil {
		return nil, false, err
	}
	sensors := make([]string, 0, maxAdGuardHistorySensors)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, false, err
		}
		sensors = append(sensors, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	if len(sensors) > maxAdGuardHistorySensors {
		return nil, false, ErrEvidenceBatchQueryLimit
	}
	all := make([]domain.Observation, 0, MaxDeviceDNSHistory)
	truncated := false
	for _, sensorID := range sensors {
		page, err := s.ListObservations(ctx, ObservationQuery{ScopeID: scopeID, SensorID: sensorID, Kind: "dns-query-observed", Since: asOf.Add(-24 * time.Hour), Until: asOf, Limit: MaxDeviceDNSHistory})
		if err != nil {
			return nil, false, err
		}
		all = append(all, page.Observations...)
		truncated = truncated || page.Next != nil
	}
	sort.Slice(all, func(i, j int) bool { return observationBefore(all[i], all[j]) })
	if len(all) > MaxDeviceDNSHistory {
		all = all[:MaxDeviceDNSHistory]
		truncated = true
	}
	return all, truncated, nil
}

func matchesDeviceDNSClaim(evidence []DeviceIdentityEvidence, clientIP string, at time.Time) bool {
	ip, err := netip.ParseAddr(clientIP)
	if err != nil || ip.Is4In6() {
		return false
	}
	kind := domain.ClaimIPv6
	if ip.Is4() {
		kind = domain.ClaimIPv4
	}
	for _, item := range evidence {
		if item.Kind != kind || item.Value != ip.String() || item.Authority != domain.LinkInferred || !strings.HasPrefix(item.Reason, "device-watch:") || !strings.HasSuffix(item.Reason, ":ip") || item.ObservedAt.After(at) || at.Sub(item.ObservedAt) > dnsIdentityWindow || item.ClaimValidUntil == nil || item.LinkValidUntil == nil || item.ClaimValidUntil.Before(at) || item.LinkValidUntil.Before(at) {
			continue
		}
		return true
	}
	return false
}

func validDNSHistoryPayload(payload dnsObservationPayload) bool {
	ip, err := netip.ParseAddr(payload.ClientIP)
	if err != nil || ip.Is4In6() || len(payload.Name) == 0 || len(payload.Name) > 253 || strings.TrimSpace(payload.Name) != payload.Name || strings.ContainsAny(payload.Name, "\x00\r\n") || len(payload.QueryType) == 0 || len(payload.QueryType) > 16 {
		return false
	}
	return payload.Filtering == "blocked" || payload.Filtering == "not-blocked" || payload.Filtering == "unknown"
}
