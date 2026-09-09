package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizeClaimValueCanonicalizesAddresses(t *testing.T) {
	ipv6, err := NormalizeClaimValue(ClaimIPv6, "2001:0db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if ipv6 != "2001:db8::1" {
		t.Fatalf("unexpected IPv6 canonical form %q", ipv6)
	}

	mac, err := NormalizeClaimValue(ClaimMAC, "AA-BB-CC-DD-EE-FF")
	if err != nil {
		t.Fatal(err)
	}
	if mac != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("unexpected MAC canonical form %q", mac)
	}
}

func TestIdentityClaimRequiresNetworkScopeAndTemporalBounds(t *testing.T) {
	now := time.Now().UTC()
	confidence := 0.8
	claim := IdentityClaim{
		ID:             "claim.1",
		ScopeID:        "scope.home",
		Kind:           ClaimIPv4,
		Value:          "192.0.2.10",
		ObservedAt:     now,
		Confidence:     &confidence,
		SourceSensorID: "sensor.desktop",
		Retention:      RetentionStandard,
	}
	if err := ValidateIdentityClaim(claim); err != nil {
		t.Fatal(err)
	}

	bad := claim
	bad.ValidUntil = ptrTime(now.Add(-time.Second))
	if err := ValidateIdentityClaim(bad); err == nil || !strings.Contains(err.Error(), "precedes") {
		t.Fatalf("invalid temporal claim accepted: %v", err)
	}
}

func TestObservationAcceptsOutOfOrderSourceTimeButRejectsUnsafePayload(t *testing.T) {
	now := time.Now().UTC()
	source := now.Add(5 * time.Minute)
	observation := Observation{
		ID:            "obs.1",
		ScopeID:       "scope.home",
		SensorID:      "sensor.desktop",
		Kind:          "neighbor-seen",
		SourceStream:  "neighbor-cache",
		SourceKey:     "entry-1",
		SourceTime:    &source,
		IngestedAt:    now,
		SchemaVersion: 1,
		Attribution:   "desktop-neighbor-cache",
		Payload:       json.RawMessage(`{"address":"192.0.2.10"}`),
		Retention:     RetentionStandard,
	}
	if err := ValidateObservation(observation); err != nil {
		t.Fatalf("source clock skew should remain representable: %v", err)
	}

	bad := observation
	bad.Payload = json.RawMessage(`{"unterminated"`)
	if err := ValidateObservation(bad); err == nil {
		t.Fatal("malformed observation payload accepted")
	}
}

func TestDeviceClaimLinkPreservesUserAndInferredAuthority(t *testing.T) {
	now := time.Now().UTC()
	for _, authority := range []LinkAuthority{LinkInferred, LinkUser} {
		link := DeviceClaimLink{
			ID:        "link.1",
			DeviceID:  "device.1",
			ClaimID:   "claim.1",
			ValidFrom: now,
			Authority: authority,
			Reason:    "fixture",
			CreatedAt: now,
		}
		if err := ValidateDeviceClaimLink(link); err != nil {
			t.Fatalf("authority %q rejected: %v", authority, err)
		}
	}
}

func TestJSONEnvelopeIsBounded(t *testing.T) {
	now := time.Now().UTC()
	observation := Observation{
		ID:            "obs.1",
		ScopeID:       "scope.home",
		SensorID:      "sensor.desktop",
		Kind:          "neighbor-seen",
		SourceStream:  "neighbor-cache",
		SourceKey:     "entry-1",
		IngestedAt:    now,
		SchemaVersion: 1,
		Attribution:   "desktop-neighbor-cache",
		Payload:       json.RawMessage(`"` + strings.Repeat("x", MaxJSONBytes) + `"`),
		Retention:     RetentionStandard,
	}
	if err := ValidateObservation(observation); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized payload accepted: %v", err)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
