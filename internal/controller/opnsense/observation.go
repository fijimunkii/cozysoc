package opnsense

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const NeighborObservationKind = "router-neighbor-reported"

type ObservationStats struct {
	Selected     int
	OutsideScope int
	Duplicate    int
}

// BuildObservations retains only router-reported neighbors inside one selected
// enrolled scope. These records are evidence from the router, not verified
// device identity, current presence, or a network-coverage heartbeat.
func BuildObservations(snapshot Snapshot, scopeID, sensorID string, prefixes []netip.Prefix, capturedAt time.Time) ([]domain.Observation, ObservationStats, error) {
	if scopeID == "" || sensorID == "" || capturedAt.IsZero() || len(prefixes) == 0 || len(prefixes) > 32 ||
		len(snapshot.Neighbors) > 2*MaxNeighbors || snapshot.IPv4Total < 0 || snapshot.IPv6Total < 0 ||
		snapshot.IPv4Total+snapshot.IPv6Total < len(snapshot.Neighbors) {
		return nil, ObservationStats{}, ErrResponse
	}
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			return nil, ObservationStats{}, ErrResponse
		}
	}
	capturedAt = capturedAt.UTC()
	bucket := capturedAt.Truncate(5 * time.Minute)
	result := make([]domain.Observation, 0, len(snapshot.Neighbors))
	seen := make(map[string]bool, len(snapshot.Neighbors))
	var stats ObservationStats
	var ipv4Read, ipv6Read int
	for _, neighbor := range snapshot.Neighbors {
		ip := neighbor.IP
		mac, err := net.ParseMAC(neighbor.MAC)
		if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.Is4In6() || ip.Zone() != "" ||
			(neighbor.Family == "ipv4") != ip.Is4() || (neighbor.Family != "ipv4" && neighbor.Family != "ipv6") ||
			err != nil || len(mac) != 6 || mac[0]&1 != 0 || (mac[0]|mac[1]|mac[2]|mac[3]|mac[4]|mac[5]) == 0 ||
			!interfaceName.MatchString(neighbor.Interface) {
			return nil, ObservationStats{}, ErrResponse
		}
		if neighbor.Family == "ipv4" {
			ipv4Read++
		} else {
			ipv6Read++
		}
		if ipv4Read > MaxNeighbors || ipv6Read > MaxNeighbors || ipv4Read > snapshot.IPv4Total || ipv6Read > snapshot.IPv6Total {
			return nil, ObservationStats{}, ErrResponse
		}
		inScope := false
		for _, prefix := range prefixes {
			if prefix.Contains(ip) {
				inScope = true
				break
			}
		}
		if !inScope {
			stats.OutsideScope++
			continue
		}
		keyInput, err := json.Marshal([]string{scopeID, sensorID, ip.String(), mac.String(), neighbor.Interface, neighbor.Family, bucket.Format(time.RFC3339Nano)})
		if err != nil {
			return nil, ObservationStats{}, ErrResponse
		}
		digest := sha256.Sum256(keyInput)
		key := hex.EncodeToString(digest[:])
		if seen[key] {
			stats.Duplicate++
			continue
		}
		seen[key] = true
		payload, err := json.Marshal(struct {
			SchemaVersion int    `json:"schema_version"`
			Address       string `json:"address"`
			Hardware      string `json:"hardware_address"`
			Interface     string `json:"interface"`
			Family        string `json:"family"`
		}{1, ip.String(), mac.String(), neighbor.Interface, neighbor.Family})
		if err != nil {
			return nil, ObservationStats{}, ErrResponse
		}
		observation := domain.Observation{ID: "obs.opnsense." + key[:32], ScopeID: scopeID, SensorID: sensorID,
			Kind: NeighborObservationKind, SourceStream: "opnsense-neighbors-v1", SourceKey: key,
			SourceTime: &capturedAt, IngestedAt: capturedAt, SchemaVersion: 1,
			Attribution: "opnsense:neighbor-table;device-identity-unverified", Payload: payload,
			Retention: domain.RetentionEphemeral}
		if err := domain.ValidateObservation(observation); err != nil {
			return nil, ObservationStats{}, ErrResponse
		}
		result = append(result, observation)
		stats.Selected++
	}
	return result, stats, nil
}
