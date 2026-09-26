package adguard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

const QueryObservationKind = "dns-query-observed"
const QueryAgeWindow = 24 * time.Hour

var ErrObservationScope = errors.New("AdGuard Home observation scope is invalid")

type ObservationStats struct {
	Selected        int
	Duplicate       int
	OutsideScope    int
	WithoutClientIP int
	OutsideWindow   int
}

// BuildObservations admits only recent requests with a visible client IP inside
// the explicitly enrolled prefixes. It makes no identity or coverage claim.
// The caller is responsible for verifying that scopeID and prefixes belong to
// a currently enrolled network before reading private query history.
func BuildObservations(snapshot Snapshot, scopeID, sensorID string, prefixes []netip.Prefix, now time.Time) ([]domain.Observation, ObservationStats, error) {
	if scopeID == "" || sensorID == "" || now.IsZero() || len(prefixes) == 0 || len(prefixes) > 32 || len(snapshot.Queries) > MaxQueryLogEntries || (!snapshot.Status.QueryLogEnabled && len(snapshot.Queries) != 0) {
		return nil, ObservationStats{}, ErrObservationScope
	}
	for _, prefix := range prefixes {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			return nil, ObservationStats{}, ErrObservationScope
		}
	}
	now = now.UTC()
	result := make([]domain.Observation, 0, len(snapshot.Queries))
	var stats ObservationStats
	seen := make(map[string]bool, len(snapshot.Queries))
	for _, query := range snapshot.Queries {
		filtering, validReason := filteringReason(query.Reason)
		if !validQuestionName(query.Name) || !validToken(query.Type, 16) || (query.Status != "" && !validToken(query.Status, 32)) || !validClientID(query.ClientID) || !validReason || filtering != query.Filtering {
			return nil, ObservationStats{}, ErrResponse
		}
		if query.ClientIP == "" || snapshot.Status.AnonymizedClients {
			stats.WithoutClientIP++
			continue
		}
		clientIP, err := netip.ParseAddr(query.ClientIP)
		if err != nil {
			return nil, ObservationStats{}, ErrResponse
		}
		inScope := false
		for _, prefix := range prefixes {
			if prefix.Contains(clientIP) {
				inScope = true
				break
			}
		}
		if !inScope {
			stats.OutsideScope++
			continue
		}
		if query.Time.IsZero() || query.Time.After(now) || query.Time.Before(now.Add(-QueryAgeWindow)) {
			stats.OutsideWindow++
			continue
		}
		// The source key is opaque: private query names and client addresses do
		// not enter indexes, sensor metadata, or diagnostics.
		keyInput, err := json.Marshal([]string{sensorID, query.Time.UTC().Format(time.RFC3339Nano), query.Name, query.Type, query.ClientIP, query.ClientID, query.Status, query.Reason})
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
			Name          string `json:"name"`
			QueryType     string `json:"query_type"`
			ClientIP      string `json:"client_ip"`
			Status        string `json:"status,omitempty"`
			Filtering     string `json:"filtering"`
			Reason        string `json:"reason,omitempty"`
		}{1, query.Name, query.Type, query.ClientIP, query.Status, query.Filtering, query.Reason})
		if err != nil {
			return nil, ObservationStats{}, ErrResponse
		}
		sourceTime := query.Time.UTC()
		observation := domain.Observation{ID: "obs.adguard." + key[:32], ScopeID: scopeID, SensorID: sensorID,
			Kind: QueryObservationKind, SourceStream: "adguard-querylog-v1", SourceKey: key,
			SourceTime: &sourceTime, IngestedAt: now, SchemaVersion: 1,
			Attribution: "resolver-client-ip;device-identity-unverified", Payload: payload,
			Retention: domain.RetentionEphemeral}
		if err := domain.ValidateObservation(observation); err != nil {
			return nil, ObservationStats{}, ErrObservationScope
		}
		result = append(result, observation)
		stats.Selected++
	}
	return result, stats, nil
}
