package coverage_test

import (
	"encoding/json"
	"testing"
	"time"

	sharedcoverage "github.com/fijimunkii/cozysoc/internal/controller/coverage"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/domain"
)

type transitionScenario struct {
	name                 string
	report               devicewatch.CoverageReport
	operational          devicewatch.OperationalHealth
	quality              domain.Observation
	securityObservations []domain.Observation
	findings             []domain.Finding
	wantCoverageState    sharedcoverage.State
	wantCoverageReason   string
	wantQualityStatus    string
	wantFindingCount     int
	wantSensorState      devicewatch.OperationalState
	wantPipelineState    devicewatch.OperationalState
}

func TestCoverageTransitionMatrixKeepsConcernBoundariesSeparate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	securityObservation, finding := securityFixture(now)

	disconnected := healthyOperationalFixture()
	disconnected.Sensor = devicewatch.SensorHealth{
		State:    devicewatch.OperationalDisconnected,
		Running:  false,
		NextStep: "Restart Device Watch before trusting current coverage.",
	}

	lagging := healthyOperationalFixture()
	lagging.Pipeline = devicewatch.PipelineHealth{
		State:    devicewatch.OperationalDegraded,
		Reason:   "latency",
		NextStep: "Restore ingestion throughput before trusting newly accepted evidence.",
	}

	tests := []transitionScenario{
		{
			name:               "healthy limited coverage with current network quality",
			report:             deviceWatchReportFixture(now, devicewatch.CoverageSourceCurrent, 2),
			operational:        healthyOperationalFixture(),
			quality:            qualityFixture(now, "current", 12),
			wantCoverageState:  sharedcoverage.StateActiveLimited,
			wantCoverageReason: "fresh-limited",
			wantQualityStatus:  "current",
			wantFindingCount:   0,
			wantSensorState:    devicewatch.OperationalCurrent,
			wantPipelineState:  devicewatch.OperationalCurrent,
		},
		{
			name:                 "security finding does not degrade observation coverage",
			report:               deviceWatchReportFixture(now, devicewatch.CoverageSourceCurrent, 2),
			operational:          healthyOperationalFixture(),
			quality:              qualityFixture(now, "current", 12),
			securityObservations: []domain.Observation{securityObservation},
			findings:             []domain.Finding{finding},
			wantCoverageState:    sharedcoverage.StateActiveLimited,
			wantCoverageReason:   "fresh-limited",
			wantQualityStatus:    "current",
			wantFindingCount:     1,
			wantSensorState:      devicewatch.OperationalCurrent,
			wantPipelineState:    devicewatch.OperationalCurrent,
		},
		{
			name:               "poor network quality is not a security finding or visibility failure",
			report:             deviceWatchReportFixture(now, devicewatch.CoverageSourceCurrent, 2),
			operational:        healthyOperationalFixture(),
			quality:            qualityFixture(now, "degraded", 350),
			wantCoverageState:  sharedcoverage.StateActiveLimited,
			wantCoverageReason: "fresh-limited",
			wantQualityStatus:  "degraded",
			wantFindingCount:   0,
			wantSensorState:    devicewatch.OperationalCurrent,
			wantPipelineState:  devicewatch.OperationalCurrent,
		},
		{
			name:               "sensor disconnection degrades coverage without inventing a finding",
			report:             deviceWatchReportFixture(now, devicewatch.CoverageSourceCurrent, 2),
			operational:        disconnected,
			quality:            qualityFixture(now, "current", 12),
			wantCoverageState:  sharedcoverage.StateDisconnected,
			wantCoverageReason: "sensor-disconnected",
			wantQualityStatus:  "current",
			wantFindingCount:   0,
			wantSensorState:    devicewatch.OperationalDisconnected,
			wantPipelineState:  devicewatch.OperationalCurrent,
		},
		{
			name:               "source gap degrades visibility without changing network quality",
			report:             deviceWatchReportFixture(now, devicewatch.CoverageSourceUnavailable, 1),
			operational:        healthyOperationalFixture(),
			quality:            qualityFixture(now, "current", 12),
			wantCoverageState:  sharedcoverage.StateDegraded,
			wantCoverageReason: "source-partial",
			wantQualityStatus:  "current",
			wantFindingCount:   0,
			wantSensorState:    devicewatch.OperationalCurrent,
			wantPipelineState:  devicewatch.OperationalCurrent,
		},
		{
			name:               "quiet network remains current when heartbeat evidence is current",
			report:             deviceWatchReportFixture(now, devicewatch.CoverageSourceCurrent, 0),
			operational:        healthyOperationalFixture(),
			quality:            qualityFixture(now, "current", 12),
			wantCoverageState:  sharedcoverage.StateActiveLimited,
			wantCoverageReason: "fresh-limited",
			wantQualityStatus:  "current",
			wantFindingCount:   0,
			wantSensorState:    devicewatch.OperationalCurrent,
			wantPipelineState:  devicewatch.OperationalCurrent,
		},
		{
			name:                 "simultaneous lag poor quality and finding remain separately inspectable",
			report:               deviceWatchReportFixture(now, devicewatch.CoverageSourceCurrent, 2),
			operational:          lagging,
			quality:              qualityFixture(now, "degraded", 350),
			securityObservations: []domain.Observation{securityObservation},
			findings:             []domain.Finding{finding},
			wantCoverageState:    sharedcoverage.StateDegraded,
			wantCoverageReason:   "ingestion-latency",
			wantQualityStatus:    "degraded",
			wantFindingCount:     1,
			wantSensorState:      devicewatch.OperationalCurrent,
			wantPipelineState:    devicewatch.OperationalDegraded,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := domain.ValidateObservation(test.quality); err != nil {
				t.Fatalf("quality fixture is not a valid normalized observation: %v", err)
			}
			for _, observation := range test.securityObservations {
				if err := domain.ValidateObservation(observation); err != nil {
					t.Fatalf("security evidence fixture is not a valid normalized observation: %v", err)
				}
			}
			for _, finding := range test.findings {
				if err := domain.ValidateFinding(finding); err != nil {
					t.Fatalf("security finding fixture is not valid: %v", err)
				}
				for _, evidenceID := range finding.EvidenceObservationIDs {
					if evidenceID == test.quality.ID {
						t.Fatal("security finding incorrectly uses network-quality evidence as its security evidence")
					}
				}
			}

			contract, err := devicewatch.CoverageContract(test.report, test.operational)
			if err != nil {
				t.Fatal(err)
			}
			if contract.State != test.wantCoverageState || contract.Reason != test.wantCoverageReason {
				t.Fatalf("coverage = %s/%s, want %s/%s", contract.State, contract.Reason, test.wantCoverageState, test.wantCoverageReason)
			}
			if len(contract.ObservationPoints) != 1 {
				t.Fatalf("coverage points = %d, want 1", len(contract.ObservationPoints))
			}
			if test.operational.Sensor.State != test.wantSensorState || test.operational.Pipeline.State != test.wantPipelineState {
				t.Fatalf("operational state = sensor:%s pipeline:%s, want sensor:%s pipeline:%s", test.operational.Sensor.State, test.operational.Pipeline.State, test.wantSensorState, test.wantPipelineState)
			}
			if got := qualityStatus(t, test.quality); got != test.wantQualityStatus {
				t.Fatalf("quality status = %q, want %q", got, test.wantQualityStatus)
			}
			if len(test.findings) != test.wantFindingCount {
				t.Fatalf("finding count = %d, want %d", len(test.findings), test.wantFindingCount)
			}
		})
	}
}

func deviceWatchReportFixture(now time.Time, ndpState devicewatch.CoverageSourceState, neighbors int) devicewatch.CoverageReport {
	state := devicewatch.CoverageActiveLimited
	reason := "fresh-limited"
	nextStep := "Use Traffic Watch when packet or flow visibility is required."
	ndpNextStep := ""
	ndpAvailable := true
	if ndpState != devicewatch.CoverageSourceCurrent {
		state = devicewatch.CoverageDegraded
		reason = "source-partial"
		nextStep = "Restore the unavailable IPv6 neighbor source before relying on dual-stack visibility."
		ndpNextStep = "Restore NDP neighbor-table access before relying on IPv6 visibility."
		ndpAvailable = false
	}
	return devicewatch.CoverageReport{
		ScopeID:          "scope.home",
		SensorID:         "sensor.dw.test",
		InterfaceName:    "en0",
		State:            state,
		Reason:           reason,
		HasEvidence:      true,
		EvidenceAt:       now.Add(-time.Minute),
		FreshUntil:       now.Add(2 * time.Minute),
		NeighborsInScope: neighbors,
		Sources: []devicewatch.CoverageSourceDetail{
			{ID: string(devicewatch.MethodARPCache), AddressFamily: "ipv4", State: devicewatch.CoverageSourceCurrent, Observed: true, AvailableAtLastSample: true},
			{ID: string(devicewatch.MethodNDPCache), AddressFamily: "ipv6", State: ndpState, Observed: true, AvailableAtLastSample: ndpAvailable, NextStep: ndpNextStep},
		},
		BlindSpots: []devicewatch.CoverageBlindSpot{{
			ID:       "no-traffic-monitoring",
			Summary:  "Device Watch does not observe other devices' traffic",
			Detail:   "Neighbor-cache evidence does not establish packet, flow, or east-west traffic visibility.",
			NextStep: "Use a validated traffic observation point when packet or flow visibility is required.",
		}},
		NextStep: nextStep,
	}
}

func healthyOperationalFixture() devicewatch.OperationalHealth {
	return devicewatch.OperationalHealth{
		Sensor:   devicewatch.SensorHealth{State: devicewatch.OperationalCurrent, Running: true},
		Pipeline: devicewatch.PipelineHealth{State: devicewatch.OperationalCurrent},
		Database: devicewatch.DatabaseHealth{State: devicewatch.OperationalCurrent},
	}
}

func qualityFixture(now time.Time, status string, latencyMS int) domain.Observation {
	payload, _ := json.Marshal(map[string]any{
		"metric":     "gateway-rtt-ms",
		"status":     status,
		"latency_ms": latencyMS,
	})
	return domain.Observation{
		ID:            "obs.quality.gateway-rtt",
		ScopeID:       "scope.home",
		SensorID:      "sensor.quality.test",
		Kind:          "network-quality",
		SourceStream:  "network-quality",
		SourceKey:     "gateway-rtt:current",
		IngestedAt:    now,
		SchemaVersion: domain.SchemaVersion,
		Attribution:   "controller-local-quality-probe",
		Payload:       payload,
		Retention:     domain.RetentionEphemeral,
	}
}

func securityFixture(now time.Time) (domain.Observation, domain.Finding) {
	observation := domain.Observation{
		ID:            "obs.security.connection-scan",
		ScopeID:       "scope.home",
		SensorID:      "sensor.security.test",
		Kind:          "connection-pattern",
		SourceStream:  "security-detector-input",
		SourceKey:     "connection-scan:fixture",
		IngestedAt:    now,
		SchemaVersion: domain.SchemaVersion,
		Attribution:   "fixture-security-sensor",
		Payload:       json.RawMessage(`{"connection_attempts":32,"window_seconds":30}`),
		Retention:     domain.RetentionShort,
	}
	confidence := 0.8
	finding := domain.Finding{
		ID:                     "finding.connection-scan",
		ScopeID:                "scope.home",
		DetectorID:             "detector.connection-scan",
		DetectorVersion:        "1.0.0",
		Category:               "network-scan",
		Severity:               "medium",
		Confidence:             &confidence,
		ObservedAt:             now,
		CreatedAt:              now,
		SchemaVersion:          domain.SchemaVersion,
		Payload:                json.RawMessage(`{"summary":"connection pattern crossed the fixture detector threshold"}`),
		EvidenceObservationIDs: []string{observation.ID},
		Retention:              domain.RetentionStandard,
	}
	return observation, finding
}

func qualityStatus(t *testing.T, observation domain.Observation) string {
	t.Helper()
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(observation.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Status
}
