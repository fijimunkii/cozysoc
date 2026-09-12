package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type gatewayHistoryReader interface {
	ReadGatewayHistory(context.Context, storage.GatewayHistoryQuery) (storage.GatewayHistoryPage, error)
}

// GatewayHistory resolves storage enrollment only, even with execution disabled
// or an offline interface. No route/OS reader, coordinator or sender is called.
func (h *controllerAPIHandler) GatewayHistory(ctx context.Context, params api.GatewayHistoryParams) (api.GatewayHistory, error) {
	if params.RunID != "" && !gatewayrun.ValidRunID(params.RunID) {
		return api.GatewayHistory{}, localapi.ErrInvalidRead
	}
	if h == nil || h.store == nil || h.now == nil {
		return api.GatewayHistory{}, gatewayrun.ErrHistory
	}
	asOf := h.now().Round(0).UTC()
	if asOf.IsZero() || !time.Unix(0, asOf.UnixNano()).Equal(asOf) {
		return api.GatewayHistory{}, gatewayrun.ErrHistory
	}
	out := api.GatewayHistory{SchemaVersion: 1, Mode: "retained-history", AsOf: asOf, LookupRunID: params.RunID,
		Limit: storage.MaxGatewayHistoryRuns, ScanLimit: storage.MaxGatewayHistoryScan, Runs: []api.GatewayHistoryRun{},
		Limitations: []string{
			"Read-only retained history: no traffic is sent, no approval is restored, and no current connectivity or security state is inferred.",
			"Only the currently enrolled scope is selected from storage; historical source/interface fields do not assert the device is still on that network. Gateway role remains unverified.",
			"The recent list scans a bounded audit window. Truncation, retention and missing terminal records must not be interpreted as no activity, no traffic, or success. Use a run reference for a retained result outside this list.",
			"Reply loss describes only completed ICMP request windows at measurement time. Missing latency stays unknown; partial samples do not establish loss. A stored result does not prove an earlier client received it.",
		}}
	if params.RunID == "" {
		since := asOf.Add(-storage.GatewayHistoryWindow)
		if !time.Unix(0, since.UnixNano()).Equal(since) {
			return api.GatewayHistory{}, gatewayrun.ErrHistory
		}
		out.Since = &since
	} else {
		out.Limit, out.ScanLimit = 1, 0
	}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if errors.Is(err, localapi.ErrReadTargetNotFound) && params.RunID == "" {
		return out, nil
	}
	if err != nil {
		return api.GatewayHistory{}, err
	}
	reader, ok := h.store.(gatewayHistoryReader)
	if !ok {
		return api.GatewayHistory{}, gatewayrun.ErrHistory
	}
	page, err := reader.ReadGatewayHistory(ctx, storage.GatewayHistoryQuery{ScopeID: binding.ScopeID, RunID: params.RunID, AsOf: asOf})
	if errors.Is(err, storage.ErrGatewayHistoryNotFound) {
		return api.GatewayHistory{}, localapi.ErrReadTargetNotFound
	}
	if err != nil {
		return api.GatewayHistory{}, err
	}
	current, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.GatewayHistory{}, err
	}
	a, b := slices.Clone(binding.Prefixes), slices.Clone(current.Prefixes)
	slices.Sort(a)
	slices.Sort(b)
	if current.ScopeID != binding.ScopeID || current.InterfaceName != binding.InterfaceName || current.InterfaceIndex != binding.InterfaceIndex || !slices.Equal(a, b) {
		return api.GatewayHistory{}, localapi.ErrReadTargetNotFound
	}
	if len(page.Runs) > out.Limit {
		return api.GatewayHistory{}, gatewayrun.ErrHistory
	}
	out.Enrolled, out.ScopeID = true, binding.ScopeID
	out.Truncated, out.ScanTruncated = page.Truncated, page.ScanTruncated
	for _, r := range page.Runs {
		if r.ScopeID != binding.ScopeID || (params.RunID != "" && r.RunID != params.RunID) {
			return api.GatewayHistory{}, gatewayrun.ErrHistory
		}
		item := api.GatewayHistoryRun{RunID: r.RunID, AuditSchemaVersion: r.SchemaVersion, Profile: r.Profile,
			InterfaceName: r.InterfaceName, InterfaceIndex: r.InterfaceIndex, Target: r.Target, Source: r.Source,
			LastAuditAt: r.LastAuditAt, AuthorizationRetained: r.AuthorizationRetained, AdmissionRetained: r.AdmissionRetained,
			TerminalRetained: r.TerminalRetained, Outcome: r.Outcome, Reason: r.Reason,
			Assessment: api.GatewayHistoricalAssessment{State: r.Assessment.State, Confidence: r.Assessment.Confidence,
				Summary: r.Assessment.Summary, NextStep: r.Assessment.NextStep}}
		if r.Measurement != nil {
			m := api.GatewayRunMeasurement(*r.Measurement)
			if m.CompletedAt != nil {
				at := *m.CompletedAt
				m.CompletedAt = &at
			}
			if m.MeanRTTNanoseconds != nil {
				ns := *m.MeanRTTNanoseconds
				m.MeanRTTNanoseconds = &ns
			}
			item.Measurement = &m
		}
		if r.Assessment.ReplyLossPercent != nil {
			loss := *r.Assessment.ReplyLossPercent
			item.Assessment.ReplyLossPercent = &loss
		}
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}

func runGatewayHistoryCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("network-quality-history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && !gatewayrun.ValidRunID(fs.Arg(0))) {
		return fmt.Errorf("network-quality-history accepts only an optional 32-character lowercase hexadecimal run reference")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	client := localapi.NewClient(dir)
	var raw json.RawMessage
	if fs.NArg() == 0 {
		raw, err = client.Call(ctx, api.MethodGatewayHistory)
	} else {
		raw, err = client.CallWithParams(ctx, api.MethodGatewayHistory, api.GatewayHistoryParams{RunID: fs.Arg(0)})
	}
	if err != nil {
		return err
	}
	var history api.GatewayHistory
	if len(raw) > 64*1024 || json.Unmarshal(raw, &history) != nil || history.SchemaVersion != 1 || history.Mode != "retained-history" {
		return fmt.Errorf("gateway history response is invalid")
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(history)
}
