package devicewatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type CoverageState string

const (
	CoverageUnverified    CoverageState = "unverified"
	CoverageActiveLimited CoverageState = "active-limited"
	CoverageDegraded      CoverageState = "degraded"
	CoverageStale         CoverageState = "stale"
)

type CoverageSourceState string

const (
	CoverageSourceCurrent     CoverageSourceState = "current"
	CoverageSourceUnavailable CoverageSourceState = "unavailable"
	CoverageSourceStale       CoverageSourceState = "stale"
	CoverageSourceMissing     CoverageSourceState = "missing"
	CoverageSourceUnknown     CoverageSourceState = "unknown"
)

type CoverageSourceDetail struct {
	ID                    string
	AddressFamily         string
	State                 CoverageSourceState
	Observed              bool
	AvailableAtLastSample bool
	NextStep              string
}

type CoverageBlindSpot struct {
	ID       string
	Summary  string
	Detail   string
	NextStep string
}

type CoverageReport struct {
	ScopeID          string
	SensorID         string
	InterfaceName    string
	State            CoverageState
	Reason           string
	HasEvidence      bool
	EvidenceAt       time.Time
	FreshUntil       time.Time
	NeighborsInScope int
	Sources          []CoverageSourceDetail
	BlindSpots       []CoverageBlindSpot
	NextStep         string
}

type CoverageSampleReader interface {
	LatestCoverageSample(context.Context, string, string) (domain.CoverageSample, bool, error)
}

type coverageEvidenceV1 struct {
	SchemaVersion              int                    `json:"schema_version"`
	Interface                  string                 `json:"interface"`
	Sources                    []coverageEvidenceSource `json:"sources"`
	NeighborsInScope           int                    `json:"neighbors_in_scope"`
	ObservationsInserted       int                    `json:"observations_inserted"`
	ObservationsDeduplicated   int                    `json:"observations_deduplicated"`
	WholeNetworkTrafficVisible bool                   `json:"whole_network_traffic_visible"`
	Limitations                []string               `json:"limitations"`
}

type coverageEvidenceSource struct {
	Method    NeighborMethod `json:"method"`
	Available bool           `json:"available"`
}

func CurrentCoverage(ctx context.Context, reader CoverageSampleReader, scopeID string, now time.Time) (CoverageReport, error) {
	report := CoverageReport{
		ScopeID:    scopeID,
		State:      CoverageUnverified,
		Reason:     "no-evidence",
		Sources:    missingCoverageSources(),
		BlindSpots: deviceWatchBlindSpots(),
		NextStep:   "Wait for the first Device Watch collection; if evidence does not appear, confirm the enrolled Mac is awake and Device Watch is enabled.",
	}
	if reader == nil {
		return report, nil
	}

	sample, ok, err := reader.LatestCoverageSample(ctx, scopeID, CapabilityID)
	if err != nil {
		return CoverageReport{}, err
	}
	if !ok {
		return report, nil
	}

	report.HasEvidence = true
	report.SensorID = sample.SensorID
	report.EvidenceAt = sample.EndedAt.UTC()
	report.FreshUntil = report.EvidenceAt.Add(coverageFreshnessWindow)

	evidence, err := decodeCoverageEvidence(sample.Evidence)
	if err != nil || !coverageStatusConsistent(sample.Status, evidence.Sources) {
		report.State = CoverageDegraded
		report.Reason = "invalid-evidence"
		report.Sources = unknownCoverageSources()
		report.NextStep = "Restart the controller; if invalid Device Watch evidence persists, inspect local diagnostics before trusting coverage state."
		return report, nil
	}
	report.InterfaceName = evidence.Interface
	report.NeighborsInScope = evidence.NeighborsInScope
	report.Sources = currentCoverageSources(evidence.Sources)

	now = now.UTC()
	if report.EvidenceAt.After(now.Add(coverageFutureTolerance)) {
		report.State = CoverageDegraded
		report.Reason = "clock-skew"
		for i := range report.Sources {
			report.Sources[i].State = CoverageSourceUnknown
			report.Sources[i].NextStep = "Correct the controller clock before relying on this source timestamp."
		}
		report.NextStep = "Correct the controller clock before relying on Device Watch freshness."
		return report, nil
	}
	if now.Sub(report.EvidenceAt) > coverageFreshnessWindow {
		report.State = CoverageStale
		report.Reason = "stale-evidence"
		for i := range report.Sources {
			report.Sources[i].State = CoverageSourceStale
			report.Sources[i].NextStep = "Wake or reconnect the enrolled Mac and confirm Device Watch is still enabled on this network."
		}
		report.NextStep = "Wake or reconnect the enrolled Mac and confirm Device Watch is still enabled on the enrolled network."
		return report, nil
	}

	switch sample.Status {
	case "partial":
		report.State = CoverageActiveLimited
		report.Reason = "fresh-limited"
		if hasUnavailableCoverageSource(report.Sources) {
			report.NextStep = "Restore the unavailable neighbor source if dual-stack device visibility matters; Device Watch remains limited even when both sources are current."
		} else {
			report.NextStep = "Use Traffic Watch rather than Device Watch when packet or flow visibility is required."
		}
	case "unavailable":
		report.State = CoverageDegraded
		report.Reason = "source-unavailable"
		report.NextStep = "Check local ARP/NDP neighbor-table access on the enrolled Mac and confirm the interface still matches the enrolled network."
	}
	return report, nil
}

func decodeCoverageEvidence(raw json.RawMessage) (coverageEvidenceV1, error) {
	var evidence coverageEvidenceV1
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return coverageEvidenceV1{}, fmt.Errorf("decode Device Watch coverage evidence: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return coverageEvidenceV1{}, fmt.Errorf("Device Watch coverage evidence contains multiple JSON values")
		}
		return coverageEvidenceV1{}, fmt.Errorf("decode Device Watch coverage evidence trailer: %w", err)
	}
	if evidence.SchemaVersion != 1 {
		return coverageEvidenceV1{}, fmt.Errorf("unsupported Device Watch coverage evidence schema %d", evidence.SchemaVersion)
	}
	if ValidateEnrollmentInterfaceName(evidence.Interface) != nil {
		return coverageEvidenceV1{}, fmt.Errorf("invalid Device Watch coverage interface")
	}
	if evidence.NeighborsInScope < 0 || evidence.NeighborsInScope > MaxNeighborEntries ||
		evidence.ObservationsInserted < 0 || evidence.ObservationsInserted > MaxNeighborEntries ||
		evidence.ObservationsDeduplicated < 0 || evidence.ObservationsDeduplicated > MaxNeighborEntries ||
		evidence.ObservationsInserted+evidence.ObservationsDeduplicated != evidence.NeighborsInScope {
		return coverageEvidenceV1{}, fmt.Errorf("invalid Device Watch coverage counters")
	}
	if evidence.WholeNetworkTrafficVisible {
		return coverageEvidenceV1{}, fmt.Errorf("Device Watch coverage cannot claim whole-network traffic visibility")
	}
	if len(evidence.Limitations) == 0 || len(evidence.Limitations) > 8 {
		return coverageEvidenceV1{}, fmt.Errorf("invalid Device Watch coverage limitations")
	}
	for _, limitation := range evidence.Limitations {
		if limitation == "" || len(limitation) > 512 || strings.TrimSpace(limitation) != limitation {
			return coverageEvidenceV1{}, fmt.Errorf("invalid Device Watch coverage limitation")
		}
	}
	if len(evidence.Sources) != 2 {
		return coverageEvidenceV1{}, fmt.Errorf("Device Watch coverage must report both neighbor sources")
	}
	seen := map[NeighborMethod]bool{}
	for _, source := range evidence.Sources {
		if source.Method != MethodARPCache && source.Method != MethodNDPCache {
			return coverageEvidenceV1{}, fmt.Errorf("unsupported Device Watch coverage source %q", source.Method)
		}
		if seen[source.Method] {
			return coverageEvidenceV1{}, fmt.Errorf("duplicate Device Watch coverage source %q", source.Method)
		}
		seen[source.Method] = true
	}
	if !seen[MethodARPCache] || !seen[MethodNDPCache] {
		return coverageEvidenceV1{}, fmt.Errorf("Device Watch coverage is missing a required neighbor source")
	}
	return evidence, nil
}

func coverageStatusConsistent(status string, sources []coverageEvidenceSource) bool {
	available := 0
	for _, source := range sources {
		if source.Available {
			available++
		}
	}
	switch status {
	case "partial":
		return available > 0
	case "unavailable":
		return available == 0
	default:
		return false
	}
}

func currentCoverageSources(sources []coverageEvidenceSource) []CoverageSourceDetail {
	byMethod := make(map[NeighborMethod]bool, len(sources))
	for _, source := range sources {
		byMethod[source.Method] = source.Available
	}
	return []CoverageSourceDetail{
		coverageSourceDetail(MethodARPCache, "ipv4", byMethod[MethodARPCache], true),
		coverageSourceDetail(MethodNDPCache, "ipv6", byMethod[MethodNDPCache], true),
	}
}

func missingCoverageSources() []CoverageSourceDetail {
	return []CoverageSourceDetail{
		{ID: string(MethodARPCache), AddressFamily: "ipv4", State: CoverageSourceMissing, NextStep: "Wait for a successful Device Watch collection before judging IPv4 neighbor visibility."},
		{ID: string(MethodNDPCache), AddressFamily: "ipv6", State: CoverageSourceMissing, NextStep: "Wait for a successful Device Watch collection before judging IPv6 neighbor visibility."},
	}
}

func unknownCoverageSources() []CoverageSourceDetail {
	return []CoverageSourceDetail{
		{ID: string(MethodARPCache), AddressFamily: "ipv4", State: CoverageSourceUnknown, NextStep: "Do not rely on IPv4 source health until valid coverage evidence is produced."},
		{ID: string(MethodNDPCache), AddressFamily: "ipv6", State: CoverageSourceUnknown, NextStep: "Do not rely on IPv6 source health until valid coverage evidence is produced."},
	}
}

func coverageSourceDetail(method NeighborMethod, family string, available, observed bool) CoverageSourceDetail {
	detail := CoverageSourceDetail{
		ID:                    string(method),
		AddressFamily:         family,
		Observed:              observed,
		AvailableAtLastSample: available,
	}
	if available {
		detail.State = CoverageSourceCurrent
		return detail
	}
	detail.State = CoverageSourceUnavailable
	if method == MethodARPCache {
		detail.NextStep = "Check that the enrolled Mac can read its ARP neighbor table for IPv4 visibility."
	} else {
		detail.NextStep = "Check that the enrolled Mac can read its NDP neighbor table for IPv6 visibility."
	}
	return detail
}

func hasUnavailableCoverageSource(sources []CoverageSourceDetail) bool {
	for _, source := range sources {
		if source.State == CoverageSourceUnavailable {
			return true
		}
	}
	return false
}

func deviceWatchBlindSpots() []CoverageBlindSpot {
	return []CoverageBlindSpot{
		{
			ID:       "host-neighbor-cache-only",
			Summary:  "Only peers recently resolved by this Mac can appear",
			Detail:   "A passive ARP/NDP cache is not a complete inventory of every device on the LAN.",
			NextStep: "Treat an unseen device as unknown rather than absent or safe.",
		},
		{
			ID:       "isolated-segments-not-observed",
			Summary:  "Client-isolated devices and other VLANs/subnets may be absent",
			Detail:   "Device Watch observes the enrolled local link from this Mac; it does not cross isolation or infer visibility from another segment.",
			NextStep: "Use an observation point inside isolated segments when broader device visibility is required.",
		},
		{
			ID:       "no-traffic-monitoring",
			Summary:  "Device Watch does not observe other devices' traffic",
			Detail:   "ARP/NDP neighbor evidence does not show internet uploads, east-west flows, packet contents, or application behavior.",
			NextStep: "Use Traffic Watch when packet or flow visibility is needed; do not infer traffic coverage from Device Watch.",
		},
	}
}
