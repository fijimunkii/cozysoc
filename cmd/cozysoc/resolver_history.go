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
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type resolverHistoryReader interface {
	ReadResolverHistory(context.Context, storage.ResolverHistoryQuery) (storage.ResolverHistoryPage, error)
}

// ResolverHistory resolves storage enrollment only, even with execution disabled
// or an offline interface. No route/OS reader, coordinator or sender is called.
func (h *controllerAPIHandler) ResolverHistory(ctx context.Context, params api.ResolverHistoryParams) (api.ResolverHistory, error) {
	if params.RunID != "" && !resolverrun.ValidRunID(params.RunID) {
		return api.ResolverHistory{}, localapi.ErrInvalidRead
	}
	if h == nil || h.store == nil || h.now == nil {
		return api.ResolverHistory{}, resolverrun.ErrHistory
	}
	asOf := h.now().Round(0).UTC()
	if asOf.IsZero() || !time.Unix(0, asOf.UnixNano()).Equal(asOf) {
		return api.ResolverHistory{}, resolverrun.ErrHistory
	}
	out := api.ResolverHistory{SchemaVersion: 1, Mode: "retained-history", AsOf: asOf, LookupRunID: params.RunID,
		Limit: storage.MaxResolverHistoryRuns, ScanLimit: storage.MaxResolverHistoryScan, Runs: []api.ResolverHistoryRun{},
		Limitations: []string{
			"Read-only retained history: no traffic is sent, no approval is restored, and no current connectivity or security state is inferred.",
			"Only the currently enrolled scope is selected from storage; historical observer/interface fields do not assert the device is still on that network. Resolver configuration is represented by immutable opaque references, without private endpoint/name or raw DNS content.",
			"The recent list scans a bounded audit window. Truncation, retention and missing terminal records must not be interpreted as no activity, no traffic, or success. Use a run reference for a retained result outside this list.",
			"DNS errors are responses, not packet loss. Matched-response timing does not imply successful resolution. Incomplete and uncertain requests are not timeouts. A stored result does not prove an earlier client received it.",
		}}
	if params.RunID == "" {
		since := asOf.Add(-storage.ResolverHistoryWindow)
		if !time.Unix(0, since.UnixNano()).Equal(since) {
			return api.ResolverHistory{}, resolverrun.ErrHistory
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
		return api.ResolverHistory{}, err
	}
	reader, ok := h.store.(resolverHistoryReader)
	if !ok {
		return api.ResolverHistory{}, resolverrun.ErrHistory
	}
	page, err := reader.ReadResolverHistory(ctx, storage.ResolverHistoryQuery{ScopeID: binding.ScopeID, RunID: params.RunID, AsOf: asOf})
	if errors.Is(err, storage.ErrResolverHistoryNotFound) {
		return api.ResolverHistory{}, localapi.ErrReadTargetNotFound
	}
	if err != nil {
		return api.ResolverHistory{}, err
	}
	current, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.ResolverHistory{}, err
	}
	a, b := slices.Clone(binding.Prefixes), slices.Clone(current.Prefixes)
	slices.Sort(a)
	slices.Sort(b)
	if current.ScopeID != binding.ScopeID || current.InterfaceName != binding.InterfaceName || current.InterfaceIndex != binding.InterfaceIndex || !slices.Equal(a, b) {
		return api.ResolverHistory{}, localapi.ErrReadTargetNotFound
	}
	if len(page.Runs) > out.Limit {
		return api.ResolverHistory{}, resolverrun.ErrHistory
	}
	out.Enrolled, out.ScopeID = true, binding.ScopeID
	out.Truncated, out.ScanTruncated = page.Truncated, page.ScanTruncated
	for _, r := range page.Runs {
		if r.Observer.ScopeID != binding.ScopeID || (params.RunID != "" && r.RunID != params.RunID) {
			return api.ResolverHistory{}, resolverrun.ErrHistory
		}
		item := api.ResolverHistoryRun{RunID: r.RunID, AuditSchemaVersion: r.SchemaVersion, Profile: r.Profile,
			Selection:   api.ResolverHistorySelection{ID: r.Selection.ID, ResolverID: r.Selection.ResolverID, QueryID: r.Selection.QueryID, Family: string(r.Selection.Family), Transport: string(r.Selection.Transport), QueryType: string(r.Selection.QueryType), Expect: string(r.Selection.Expect)},
			Observer:    api.ResolverHistoryObserver{ScopeID: r.Observer.ScopeID, SensorID: r.Observer.SensorID, InterfaceName: r.Observer.InterfaceName, InterfaceIndex: r.Observer.InterfaceIndex},
			LastAuditAt: r.LastAuditAt, AuthorizationRetained: r.AuthorizationRetained, AdmissionRetained: r.AdmissionRetained, TerminalRetained: r.TerminalRetained, Outcome: r.Outcome, Reason: r.Reason,
			Assessment: api.ResolverHistoricalAssessment{State: r.Assessment.State, Confidence: r.Assessment.Confidence, Summary: r.Assessment.Summary, NextStep: r.Assessment.NextStep}}
		if r.Measurement != nil {
			m := api.ResolverRunMeasurement(*r.Measurement)
			if m.Reply != nil {
				reply := *m.Reply
				m.Reply = &reply
			}
			if m.ResponseTimeNanoseconds != nil {
				ns := *m.ResponseTimeNanoseconds
				m.ResponseTimeNanoseconds = &ns
			}
			item.Measurement = &m
		}
		if r.Assessment.ExpectationMatched != nil {
			matched := *r.Assessment.ExpectationMatched
			item.Assessment.ExpectationMatched = &matched
		}
		out.Runs = append(out.Runs, item)
	}
	return out, nil
}

func runResolverHistoryCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("resolver-history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && !resolverrun.ValidRunID(fs.Arg(0))) {
		return fmt.Errorf("resolver-history accepts only an optional 32-character lowercase hexadecimal run reference")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	client := localapi.NewClient(dir)
	var raw json.RawMessage
	if fs.NArg() == 0 {
		raw, err = client.Call(ctx, api.MethodResolverHistory)
	} else {
		raw, err = client.CallWithParams(ctx, api.MethodResolverHistory, api.ResolverHistoryParams{RunID: fs.Arg(0)})
	}
	if err != nil {
		return err
	}
	var history api.ResolverHistory
	if len(raw) > 64*1024 || json.Unmarshal(raw, &history) != nil || history.SchemaVersion != 1 || history.Mode != "retained-history" {
		return fmt.Errorf("resolver history response is invalid")
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(history)
}
