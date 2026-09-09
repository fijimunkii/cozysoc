package devicewatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/domain"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

const (
	macContinuityHorizon = 7 * 24 * time.Hour
	claimValidity        = 10 * time.Minute
)

type IdentityStore interface {
	EnsureDevice(context.Context, domain.Device) error
	EnsureIdentityClaim(context.Context, domain.IdentityClaim) (string, error)
	EnsureDeviceClaimLink(context.Context, domain.DeviceClaimLink) error
	FindRecentDevicesByClaim(context.Context, string, domain.ClaimKind, string, time.Time, time.Time) ([]domain.Device, error)
}

type Reconciler struct {
	store IdentityStore
}

type ReconciliationResult struct {
	DeviceID  string `json:"device_id,omitempty"`
	Created   bool   `json:"created"`
	Ambiguous bool   `json:"ambiguous"`
	Claims    int    `json:"claims"`
}

type neighborObservationPayload struct {
	SchemaVersion   int    `json:"schema_version"`
	Address         string `json:"address"`
	HardwareAddress string `json:"hardware_address"`
	Interface       string `json:"interface"`
	Family          string `json:"family"`
	Method          string `json:"method"`
	State           string `json:"state"`
}

func NewReconciler(store IdentityStore) (*Reconciler, error) {
	if store == nil {
		return nil, fmt.Errorf("device watch identity store is required")
	}
	return &Reconciler{store: store}, nil
}

func (r *Reconciler) ReconcileObservation(ctx context.Context, observation domain.Observation) (ReconciliationResult, error) {
	if r == nil || r.store == nil {
		return ReconciliationResult{}, fmt.Errorf("device watch reconciler is unavailable")
	}
	if observation.Kind != "device-neighbor-seen" {
		return ReconciliationResult{}, fmt.Errorf("unsupported device watch observation kind %q", observation.Kind)
	}
	if err := domain.ValidateObservation(observation); err != nil {
		return ReconciliationResult{}, err
	}

	_, address, hardware, err := decodeNeighborIdentity(observation.Payload)
	if err != nil {
		return ReconciliationResult{}, err
	}
	observedAt := observation.IngestedAt.UTC()
	if observation.SourceTime != nil {
		observedAt = observation.SourceTime.UTC()
	}
	validUntil := observedAt.Add(claimValidity)

	macConfidence := 0.90
	if hardware[0]&2 != 0 {
		macConfidence = 0.75
	}
	ipConfidence := 0.65
	macValue := strings.ToLower(hardware.String())
	ipKind := domain.ClaimIPv6
	if address.Is4() {
		ipKind = domain.ClaimIPv4
	}
	ipValue := address.String()

	macClaim := domain.IdentityClaim{
		ID:                  "claim.dw.mac." + stableDigest("mac-claim-v1", observation.ID, macValue),
		ScopeID:             observation.ScopeID,
		Kind:                domain.ClaimMAC,
		Value:               macValue,
		ObservedAt:          observedAt,
		ValidUntil:          &validUntil,
		Confidence:          &macConfidence,
		SourceSensorID:      observation.SensorID,
		SourceObservationID: observation.ID,
		Retention:           domain.RetentionStandard,
	}
	ipClaim := domain.IdentityClaim{
		ID:                  "claim.dw.ip." + stableDigest("ip-claim-v1", observation.ID, ipValue),
		ScopeID:             observation.ScopeID,
		Kind:                ipKind,
		Value:               ipValue,
		ObservedAt:          observedAt,
		ValidUntil:          &validUntil,
		Confidence:          &ipConfidence,
		SourceSensorID:      observation.SensorID,
		SourceObservationID: observation.ID,
		Retention:           domain.RetentionStandard,
	}

	macClaimID, err := r.store.EnsureIdentityClaim(ctx, macClaim)
	if err != nil {
		return ReconciliationResult{}, fmt.Errorf("reconcile MAC claim: %w", err)
	}
	ipClaimID, err := r.store.EnsureIdentityClaim(ctx, ipClaim)
	if err != nil {
		return ReconciliationResult{}, fmt.Errorf("reconcile IP claim: %w", err)
	}
	result := ReconciliationResult{Claims: 2}

	candidates, err := r.store.FindRecentDevicesByClaim(ctx, observation.ScopeID, domain.ClaimMAC, macValue,
		observedAt.Add(-macContinuityHorizon), observedAt)
	if err != nil {
		return result, fmt.Errorf("find MAC continuity candidate: %w", err)
	}
	if len(candidates) > 1 {
		result.Ambiguous = true
		return result, nil
	}

	var device domain.Device
	if len(candidates) == 1 {
		device = candidates[0]
	} else {
		device = domain.Device{
			ID:        "device.dw." + stableDigest("device-v1", observation.ScopeID, observation.ID, macValue),
			CreatedAt: observedAt,
		}
		if err := r.store.EnsureDevice(ctx, device); err != nil {
			return result, fmt.Errorf("create device candidate: %w", err)
		}
		result.Created = true
	}
	result.DeviceID = device.ID

	reason := "device-watch:new-mac-candidate"
	if len(candidates) == 1 {
		reason = "device-watch:recent-mac-continuity"
	}
	for _, item := range []struct {
		claimID    string
		confidence float64
		suffix     string
	}{
		{claimID: macClaimID, confidence: macConfidence, suffix: "mac"},
		{claimID: ipClaimID, confidence: ipConfidence, suffix: "ip"},
	} {
		confidence := item.confidence
		link := domain.DeviceClaimLink{
			ID:                    "link.dw." + stableDigest("link-v1", device.ID, item.claimID),
			DeviceID:              device.ID,
			ClaimID:               item.claimID,
			ValidFrom:             observedAt,
			ValidUntil:            &validUntil,
			Confidence:            &confidence,
			Authority:             domain.LinkInferred,
			Reason:                reason + ":" + item.suffix,
			EvidenceObservationID: observation.ID,
			CreatedAt:             observedAt,
		}
		if err := r.store.EnsureDeviceClaimLink(ctx, link); err != nil {
			return result, fmt.Errorf("link device identity claim: %w", err)
		}
	}
	return result, nil
}

func decodeNeighborIdentity(raw json.RawMessage) (neighborObservationPayload, netip.Addr, net.HardwareAddr, error) {
	var payload neighborObservationPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, netip.Addr{}, nil, fmt.Errorf("decode neighbor identity observation: %w", err)
	}
	if payload.SchemaVersion != 1 || payload.Interface == "" || payload.Method == "" {
		return payload, netip.Addr{}, nil, fmt.Errorf("neighbor identity observation has invalid metadata")
	}
	address, err := netip.ParseAddr(payload.Address)
	if err != nil || !address.IsValid() || address.IsMulticast() || address.IsUnspecified() || address.IsLoopback() {
		return payload, netip.Addr{}, nil, fmt.Errorf("neighbor identity observation has invalid IP address")
	}
	if address.Is6() {
		address = address.WithZone("")
	}
	if payload.Family == "ipv4" && !address.Is4() || payload.Family == "ipv6" && !address.Is6() {
		return payload, netip.Addr{}, nil, fmt.Errorf("neighbor identity observation address family mismatch")
	}
	if payload.Family != "ipv4" && payload.Family != "ipv6" {
		return payload, netip.Addr{}, nil, fmt.Errorf("neighbor identity observation has invalid address family")
	}
	hardware, err := net.ParseMAC(payload.HardwareAddress)
	if err != nil || len(hardware) != 6 || hardware[0]&1 != 0 {
		return payload, netip.Addr{}, nil, fmt.Errorf("neighbor identity observation has invalid hardware address")
	}
	allZero := true
	for _, value := range hardware {
		if value != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return payload, netip.Addr{}, nil, fmt.Errorf("neighbor identity observation has zero hardware address")
	}
	return payload, address, hardware, nil
}

type ReconcilingSink struct {
	Evidence   EvidenceSink
	Reconciler *Reconciler
}

func (s ReconcilingSink) PutObservation(ctx context.Context, observation domain.Observation) (bool, error) {
	if s.Evidence == nil || s.Reconciler == nil {
		return false, fmt.Errorf("device watch reconciling sink is incomplete")
	}
	inserted, err := s.Evidence.PutObservation(ctx, observation)
	if err != nil {
		return false, err
	}
	if _, err := s.Reconciler.ReconcileObservation(ctx, observation); err != nil {
		return inserted, err
	}
	return inserted, nil
}

func (s ReconcilingSink) PutCoverageSample(ctx context.Context, sample domain.CoverageSample) error {
	if s.Evidence == nil {
		return fmt.Errorf("device watch evidence sink is unavailable")
	}
	return s.Evidence.PutCoverageSample(ctx, sample)
}

var _ IdentityStore = (*storage.Store)(nil)
