package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/devicewatch"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/networkquality"
)

// PreviewGatewayCheck reviews ONE selected address without transmitting traffic,
// resolving names, reading neighbor caches, enabling monitoring, or storing a
// grant. Only the controller chooses the enrolled scope and interface.
func (h *controllerAPIHandler) PreviewGatewayCheck(ctx context.Context, params api.GatewayPlanParams) (api.GatewayCheckPlan, error) {
	if err := ctx.Err(); err != nil {
		return api.GatewayCheckPlan{}, err
	}
	if networkquality.ValidateGatewayPreviewTarget(params.Target) != nil {
		return api.GatewayCheckPlan{}, localapi.ErrInvalidRead
	}
	if h.store == nil || h.networkInspector == nil || h.now == nil {
		return api.GatewayCheckPlan{}, fmt.Errorf("gateway preview service is unavailable")
	}
	scopes, err := h.store.ListActiveDeviceWatchScopes(ctx)
	if err != nil {
		return api.GatewayCheckPlan{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.GatewayCheckPlan{}, err
	}
	if len(scopes) == 0 {
		return api.GatewayCheckPlan{}, localapi.ErrReadTargetNotFound
	}
	if len(scopes) != 1 {
		return api.GatewayCheckPlan{}, fmt.Errorf("gateway preview requires one enrolled scope")
	}
	binding, err := devicewatch.ParseScopeBinding(scopes[0])
	if err != nil {
		return api.GatewayCheckPlan{}, fmt.Errorf("gateway preview enrollment is invalid")
	}
	selected := networkquality.GatewayPlanBinding{
		ScopeID: scopes[0].ID, InterfaceName: binding.InterfaceName,
		InterfaceIndex: binding.InterfaceIndex, Prefixes: binding.Prefixes,
	}
	startedAt := h.now().UTC()
	// Reject ineligible target/context before touching OS metadata.
	if _, err := networkquality.PreviewGatewayCheck(selected, params.Target, startedAt); err != nil {
		if errors.Is(err, networkquality.ErrGatewayPlanTarget) {
			return api.GatewayCheckPlan{}, localapi.ErrInvalidRead
		}
		return api.GatewayCheckPlan{}, err
	}
	if err := ctx.Err(); err != nil {
		return api.GatewayCheckPlan{}, err
	}
	state, inspectErr := h.networkInspector.Inspect(ctx, binding.InterfaceName)
	if err := ctx.Err(); err != nil {
		return api.GatewayCheckPlan{}, err
	}
	if errors.Is(inspectErr, context.Canceled) || errors.Is(inspectErr, context.DeadlineExceeded) {
		return api.GatewayCheckPlan{}, inspectErr
	}
	// Share #100's complete-prefix and interface-identity comparison. Do not use
	// discovery's any-prefix-match preflight as authority for an active check.
	outcome, gap := localInterfaceOutcome(binding, state, inspectErr)
	if outcome != networkquality.OutcomeSucceeded || gap != networkquality.GapNone {
		return api.GatewayCheckPlan{}, localapi.ErrGatewayPlanPrecondition
	}
	now := h.now().UTC()
	if now.Before(startedAt) || now.Sub(startedAt) > 5*time.Second {
		return api.GatewayCheckPlan{}, fmt.Errorf("gateway preview sample time is invalid")
	}
	plan, err := networkquality.PreviewGatewayCheck(selected, params.Target, now)
	if err != nil {
		return api.GatewayCheckPlan{}, err
	}
	result := projectGatewayCheckPlan(plan)
	result.Route, err = h.reviewGatewayRoute(ctx, plan)
	if err != nil {
		return api.GatewayCheckPlan{}, err
	}
	finished := h.now().UTC()
	if finished.Before(now) || finished.Sub(startedAt) > 5*time.Second {
		return api.GatewayCheckPlan{}, fmt.Errorf("gateway preview sample time is invalid")
	}
	return result, nil
}

func projectGatewayCheckPlan(plan networkquality.GatewayCheckPlan) api.GatewayCheckPlan {
	return api.GatewayCheckPlan{
		SchemaVersion: 1, Mode: "preview-only", ExecutionAvailable: false, ConsentGranted: false,
		CreatedAt: plan.CreatedAt, ReviewExpiresAt: plan.ReviewExpiresAt,
		Binding: api.GatewayPlanBinding{
			ScopeID: plan.Binding.ScopeID, InterfaceName: plan.Binding.InterfaceName,
			InterfaceIndex: plan.Binding.InterfaceIndex, Prefixes: append([]string(nil), plan.Binding.Prefixes...),
		},
		Target: api.GatewayPlanTarget{Address: plan.Target.String(), Family: "ipv4", Role: "user-selected-gateway", RoleVerified: false},
		Method: "icmp-echo",
		ProposedBudget: api.GatewayPlanBudget{
			MaxAttempts: plan.Budget.MaxAttempts, MinIntervalMS: plan.Budget.MinInterval.Milliseconds(),
			AttemptTimeoutMS: plan.Budget.AttemptTimeout.Milliseconds(), TotalTimeoutMS: plan.Budget.TotalTimeout.Milliseconds(),
			PayloadBytes: plan.Budget.PayloadBytes, MaxICMPRequestBytes: plan.Budget.MaxICMPRequestBytes,
			MaxConcurrentRuns: plan.Budget.MaxConcurrentRuns, MinRunIntervalMS: plan.Budget.MinRunInterval.Milliseconds(),
		},
		Limitations: []string{
			"Preview only: no traffic was sent, no execution consent was recorded, and no executor is available.",
			"Route metadata is a point-in-time consistency check only; a route-associated interface address does not prove a future socket's source or egress binding. Gateway role remains unverified.",
			"These fixed limits are requirements for a future one-shot executor, not evidence that traffic limits are currently enforced.",
			"The byte ceiling counts ICMP request headers and payload only, excluding IP/link overhead, neighbor resolution, replies, and link retransmissions.",
			"Future probes may be visible to the selected destination and network infrastructure. No DNS lookup or external target is planned.",
			"An unanswered ICMP check would not by itself establish gateway failure, an internet outage, or a security finding.",
			"Interface/prefix matching cannot distinguish networks reusing the same binding. Execution will require fresh route and source verification plus explicit one-shot consent.",
		},
	}
}

func runGatewayPlanCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("network-quality-plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("network-quality-plan requires TARGET_IPV4 and never sends traffic")
	}
	if err := networkquality.ValidateGatewayPreviewTarget(fs.Arg(0)); err != nil {
		return err
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	raw, err := localapi.NewClient(dir).CallWithParams(ctx, api.MethodNetworkQualityGatewayPlan, api.GatewayPlanParams{Target: fs.Arg(0)})
	if err != nil {
		return err
	}
	var result api.GatewayCheckPlan
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode gateway preview response: %w", err)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
