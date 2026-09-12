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
	"github.com/fijimunkii/cozysoc/internal/controller/httpsrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type httpsHistoryReader interface {
	ReadHTTPSHistory(context.Context, storage.HTTPSHistoryQuery) (storage.HTTPSHistoryPage, error)
}

// HTTPSHistory resolves storage enrollment only, even with execution disabled
// or an offline interface. No route/OS reader, coordinator or sender is called.
func (h *controllerAPIHandler) HTTPSHistory(ctx context.Context, params api.HTTPSHistoryParams) (api.HTTPSHistory, error) {
	if params.RunID != "" && !httpsrun.ValidRunID(params.RunID) {
		return api.HTTPSHistory{}, localapi.ErrInvalidRead
	}
	if h == nil || h.store == nil || h.now == nil {
		return api.HTTPSHistory{}, httpsrun.ErrHistory
	}
	asOf := h.now().Round(0).UTC()
	if asOf.IsZero() || !time.Unix(0, asOf.UnixNano()).Equal(asOf) {
		return api.HTTPSHistory{}, httpsrun.ErrHistory
	}
	out := api.HTTPSHistory{SchemaVersion: 1, Mode: "retained-history", AsOf: asOf, LookupRunID: params.RunID,
		Limit: storage.MaxHTTPSHistoryRuns, ScanLimit: storage.MaxHTTPSHistoryScan, Runs: []api.HTTPSHistoryRun{},
		Limitations: []string{
			"Read-only retained history: no traffic is sent, no approval is restored, and no current connectivity or security state is inferred.",
			"Only the currently enrolled scope is selected from storage; historical observer/interface fields do not assert the device is still on that network. HTTPS configuration is represented by immutable opaque references, without private endpoint/name or TLS identity, request target or response content.",
			"The recent list scans a bounded audit window. Truncation, retention and missing terminal records must not be interpreted as no activity, no traffic, or success. Use a run reference for a retained result outside this list.",
			"HTTP errors and redirects are responses, not packet loss. Header timing includes connection and TLS work and does not imply matching status or body correctness. Incomplete and uncertain requests are not timeouts. A stored result does not prove an earlier client received it.",
		}}
	if params.RunID == "" {
		since := asOf.Add(-storage.HTTPSHistoryWindow)
		if !time.Unix(0, since.UnixNano()).Equal(since) {
			return api.HTTPSHistory{}, httpsrun.ErrHistory
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
		return api.HTTPSHistory{}, err
	}
	reader, ok := h.store.(httpsHistoryReader)
	if !ok {
		return api.HTTPSHistory{}, httpsrun.ErrHistory
	}
	page, err := reader.ReadHTTPSHistory(ctx, storage.HTTPSHistoryQuery{ScopeID: binding.ScopeID, RunID: params.RunID, AsOf: asOf})
	if errors.Is(err, storage.ErrHTTPSHistoryNotFound) {
		return api.HTTPSHistory{}, localapi.ErrReadTargetNotFound
	}
	if err != nil {
		return api.HTTPSHistory{}, err
	}
	current, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.HTTPSHistory{}, err
	}
	a, b := slices.Clone(binding.Prefixes), slices.Clone(current.Prefixes)
	slices.Sort(a)
	slices.Sort(b)
	if current.ScopeID != binding.ScopeID || current.InterfaceName != binding.InterfaceName || current.InterfaceIndex != binding.InterfaceIndex || !slices.Equal(a, b) {
		return api.HTTPSHistory{}, localapi.ErrReadTargetNotFound
	}
	if len(page.Runs) > out.Limit {
		return api.HTTPSHistory{}, httpsrun.ErrHistory
	}
	out.Enrolled, out.ScopeID = true, binding.ScopeID
	out.Truncated, out.ScanTruncated = page.Truncated, page.ScanTruncated
	for _, r := range page.Runs {
		if r.Observer.ScopeID != binding.ScopeID || (params.RunID != "" && r.RunID != params.RunID) {
			return api.HTTPSHistory{}, httpsrun.ErrHistory
		}
		item := api.HTTPSHistoryRun{RunID: r.RunID, AuditSchemaVersion: r.SchemaVersion, Profile: r.Profile,
			Selection:   api.HTTPSHistorySelection{ID: r.Selection.ID, EndpointID: r.Selection.EndpointID, RequestID: r.Selection.RequestID, Family: string(r.Selection.Family), Method: r.Selection.Method, ExpectedStatus: r.Selection.ExpectedStatus},
			Observer:    api.HTTPSHistoryObserver{ScopeID: r.Observer.ScopeID, SensorID: r.Observer.SensorID, InterfaceName: r.Observer.InterfaceName, InterfaceIndex: r.Observer.InterfaceIndex},
			LastAuditAt: r.LastAuditAt, AuthorizationRetained: r.AuthorizationRetained, AdmissionRetained: r.AdmissionRetained, TerminalRetained: r.TerminalRetained, Outcome: r.Outcome, Reason: r.Reason,
			Assessment: api.HTTPSHistoricalAssessment{State: r.Assessment.State, Confidence: r.Assessment.Confidence, Summary: r.Assessment.Summary, NextStep: r.Assessment.NextStep}}
		if r.Measurement != nil {
			m := api.HTTPSRunMeasurement(*r.Measurement)
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

func runHTTPSHistoryCommand(ctx context.Context, args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("https-history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "", "controller state directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && !httpsrun.ValidRunID(fs.Arg(0))) {
		return fmt.Errorf("https-history accepts only an optional 32-character lowercase hexadecimal run reference")
	}
	dir, err := resolveStateDir(*stateDir)
	if err != nil {
		return err
	}
	client := localapi.NewClient(dir)
	var raw json.RawMessage
	if fs.NArg() == 0 {
		raw, err = client.Call(ctx, api.MethodHTTPSHistory)
	} else {
		raw, err = client.CallWithParams(ctx, api.MethodHTTPSHistory, api.HTTPSHistoryParams{RunID: fs.Arg(0)})
	}
	if err != nil {
		return err
	}
	var history api.HTTPSHistory
	if len(raw) > 64*1024 || json.Unmarshal(raw, &history) != nil || history.SchemaVersion != 1 || history.Mode != "retained-history" {
		return fmt.Errorf("https history response is invalid")
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(history)
}
