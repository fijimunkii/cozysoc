package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

const localInterfaceFreshness = 30 * time.Second
const maxLocalInterfacePrefixes = 128

// LocalNetworkQuality reads only the enrolled interface's OS metadata. It does
// not enable Device Watch, read neighbor caches, transmit packets, or write to
// storage. Enrollment is resolved here, never from caller-supplied parameters.
func (h *controllerAPIHandler) LocalNetworkQuality(ctx context.Context) (api.LocalNetworkQuality, error) {
	if err := ctx.Err(); err != nil {
		return api.LocalNetworkQuality{}, err
	}
	if h.store == nil || h.networkInspector == nil || h.now == nil {
		return api.LocalNetworkQuality{}, fmt.Errorf("local network quality service is unavailable")
	}
	scopes, err := h.store.ListActiveDeviceWatchScopes(ctx)
	if err != nil {
		return api.LocalNetworkQuality{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.LocalNetworkQuality{}, err
	}
	if len(scopes) > 1 {
		return api.LocalNetworkQuality{}, fmt.Errorf("local network quality requires one enrolled network")
	}
	startedAt := h.now().UTC()
	if startedAt.IsZero() {
		return api.LocalNetworkQuality{}, fmt.Errorf("local network quality clock is unavailable")
	}
	result := api.LocalNetworkQuality{
		AsOf: startedAt,
		Limitations: []string{
			"This on-demand sample describes one enrolled interface, not the whole home or continuous monitoring.",
			"Administrative interface state is not physical link, gateway, DNS, or internet reachability; no probes were sent.",
			"Interface and prefix matching cannot distinguish different networks that reuse the same binding, and does not authorize active checks.",
			"Network quality is separate from security findings and monitoring coverage. This sample is not stored as history.",
		},
	}
	if len(scopes) == 0 {
		return result, nil
	}
	binding, err := devicewatch.ParseScopeBinding(scopes[0])
	if err != nil {
		return api.LocalNetworkQuality{}, fmt.Errorf("local network quality enrollment is invalid")
	}
	observer := networkquality.Observer{
		ScopeID: scopes[0].ID, SensorID: "controller-local-interface",
		InterfaceName: binding.InterfaceName, InterfaceIndex: binding.InterfaceIndex,
	}
	target := networkquality.Target{ID: "enrolled-interface", Layer: networkquality.LayerLink,
		Method: networkquality.MethodInterface, Family: networkquality.FamilyNone}
	// Validate the selected context before any OS inspection. The source ID is a
	// logical local producer identifier, not a claim that a sensor was enrolled.
	if err := networkquality.ValidateSnapshot(networkquality.Snapshot{
		Observer: observer, AsOf: startedAt, WindowStart: startedAt,
		Freshness: localInterfaceFreshness, Targets: []networkquality.Target{target},
	}); err != nil {
		return api.LocalNetworkQuality{}, fmt.Errorf("local network quality context is invalid")
	}
	state, inspectErr := h.networkInspector.Inspect(ctx, binding.InterfaceName)
	if err := ctx.Err(); err != nil {
		return api.LocalNetworkQuality{}, err
	}
	if errors.Is(inspectErr, context.Canceled) || errors.Is(inspectErr, context.DeadlineExceeded) {
		return api.LocalNetworkQuality{}, inspectErr
	}
	asOf := h.now().UTC()
	outcome, gap := localInterfaceOutcome(binding, state, inspectErr)
	measurement := networkquality.Measurement{
		ID:       "interface-" + asOf.Format("20060102t150405.000000000"),
		TargetID: target.ID, Observer: observer, StartedAt: startedAt, CompletedAt: asOf,
		Outcome: outcome, Gap: gap,
	}
	report, err := networkquality.Assess(networkquality.Snapshot{
		Observer: observer, AsOf: asOf, WindowStart: startedAt, Freshness: localInterfaceFreshness,
		Targets: []networkquality.Target{target}, Measurements: []networkquality.Measurement{measurement},
	})
	if err != nil {
		return api.LocalNetworkQuality{}, fmt.Errorf("local network quality sample is invalid")
	}
	check := report.Checks[0]
	result.Enrolled, result.AsOf = true, asOf
	result.Observer = &api.NetworkQualityObserver{
		ScopeID: observer.ScopeID, SensorID: observer.SensorID,
		InterfaceName: observer.InterfaceName, InterfaceIndex: observer.InterfaceIndex,
	}
	result.Check = &api.LocalInterfaceCheck{
		Source: "os-interface-metadata", Layer: string(target.Layer), Method: string(target.Method),
		State: string(check.State), Confidence: string(check.Confidence), EvidenceID: check.EvidenceID,
		StartedAt: check.StartedAt, CompletedAt: check.CompletedAt, FreshUntil: check.FreshUntil,
		Gap: string(check.Gap), Summary: check.Summary, NextStep: check.NextStep,
	}
	if gap == networkquality.GapNone {
		up := outcome == networkquality.OutcomeSucceeded
		result.Check.AdministrativeUp = &up
		result.Check.Summary = "The enrolled interface is administratively down."
		if up {
			result.Check.Summary = "The enrolled interface is administratively up."
		}
		result.Check.NextStep = "Review this device's connection when needed; administrative state alone does not establish a working physical link or internet connection."
	} else if gap == networkquality.GapNetworkChanged {
		result.Check.Summary = "The current interface binding no longer matches enrollment."
		result.Check.NextStep = "Review network enrollment; no interface-state conclusion was attributed to the enrolled network."
	}
	return result, nil
}

// Unlike discovery's scope preflight, this comparison allows an administratively
// down interface if the full enrolled prefix set is still present. It does not
// relax Device Watch's preflight or create authorization for any active check.
func localInterfaceOutcome(binding devicewatch.ScopeBinding, state devicewatch.InterfaceState, inspectErr error) (networkquality.Outcome, networkquality.GapReason) {
	if inspectErr != nil {
		if errors.Is(inspectErr, os.ErrPermission) {
			return networkquality.OutcomeUnavailable, networkquality.GapPermission
		}
		return networkquality.OutcomeUnavailable, networkquality.GapSourceUnavailable
	}
	if state.Name != binding.InterfaceName || state.Index != binding.InterfaceIndex {
		return networkquality.OutcomeNotRun, networkquality.GapNetworkChanged
	}
	if state.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
		return networkquality.OutcomeUnavailable, networkquality.GapUnsupported
	}
	if len(state.Prefixes) > maxLocalInterfacePrefixes {
		return networkquality.OutcomeUnavailable, networkquality.GapSourceUnavailable
	}
	expected := make(map[netip.Prefix]struct{}, len(binding.Prefixes))
	for _, raw := range binding.Prefixes {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return networkquality.OutcomeUnavailable, networkquality.GapSourceUnavailable
		}
		expected[prefix.Masked()] = struct{}{}
	}
	current := make(map[netip.Prefix]struct{}, len(state.Prefixes))
	hasNetworkPrefix := false
	for _, prefix := range state.Prefixes {
		if !prefix.IsValid() {
			return networkquality.OutcomeUnavailable, networkquality.GapSourceUnavailable
		}
		address := prefix.Masked().Addr()
		if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() {
			continue
		}
		current[prefix.Masked()] = struct{}{}
		if !address.IsLinkLocalUnicast() {
			hasNetworkPrefix = true
		}
	}
	if len(current) != len(expected) {
		return networkquality.OutcomeNotRun, networkquality.GapNetworkChanged
	}
	for prefix := range expected {
		if _, ok := current[prefix]; !ok {
			return networkquality.OutcomeNotRun, networkquality.GapNetworkChanged
		}
	}
	// Common link-local prefixes alone are insufficient binding evidence.
	if !hasNetworkPrefix {
		return networkquality.OutcomeUnavailable, networkquality.GapSourceUnavailable
	}
	if state.Flags&net.FlagUp == 0 {
		return networkquality.OutcomeFailed, networkquality.GapNone
	}
	return networkquality.OutcomeSucceeded, networkquality.GapNone
}
