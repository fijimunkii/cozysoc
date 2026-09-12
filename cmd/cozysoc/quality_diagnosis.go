package main

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/fijimunkii/cozysoc/internal/controller/api"
	"github.com/fijimunkii/cozysoc/internal/controller/gatewayrun"
	"github.com/fijimunkii/cozysoc/internal/controller/localapi"
	nq "github.com/fijimunkii/cozysoc/internal/controller/networkquality"
	"github.com/fijimunkii/cozysoc/internal/controller/resolverrun"
	"github.com/fijimunkii/cozysoc/internal/controller/storage"
)

type qualityHistoryReader interface {
	ReadQualityHistory(context.Context, string, time.Time) (storage.QualityHistoryPage, error)
}

func (h *controllerAPIHandler) QualityDiagnosis(ctx context.Context) (api.QualityDiagnosis, error) {
	if h == nil || h.store == nil || h.now == nil {
		return api.QualityDiagnosis{}, storage.ErrQualityHistory
	}
	readAt := h.now().Round(0).UTC()
	since := readAt.Add(-storage.GatewayHistoryWindow)
	if !webHistoryTime(readAt) || !webHistoryTime(since) {
		return api.QualityDiagnosis{}, storage.ErrQualityHistory
	}
	out := api.QualityDiagnosis{SchemaVersion: 1, Mode: "retained-comparison", ReadAt: readAt, Since: since, RunLimitPerLayer: storage.MaxGatewayHistoryRuns, ScanLimitPerLayer: storage.MaxGatewayHistoryScan,
		MaxCompletionSkewMS: 30000, FreshnessMS: 30000, Selected: []api.QualityDiagnosisRun{}, Compared: []api.QualityDiagnosisReference{}, Conclusion: "not-enrolled", Confidence: "unknown",
		Summary: "No enrolled network was selected for this historical comparison.", NextStep: "Review network enrollment. Enrollment does not enable monitoring or authorize active checks.",
		Limitations: []string{
			"This compares retained evidence from this controller's local audit store, not current connectivity. Reading sends no probes, collects no OS metadata and restores no approval.",
			"Only the latest retained gateway run and resolver run in the enrolled scope are selected. Missing, incomplete or newer unknown results never revive older successes.",
			"Read time is separate from the historical assessment anchor. Original sample times and execution outcomes are retained; completion is not proof of successful connectivity or resolution.",
			"A shared local store supplies the observation-device reference. Stored interface metadata cannot prove an unchanged route or network. These run audits do not provide a complete controller-lifetime or binding-change timeline.",
			"The ICMP target's gateway role is unverified. DNS errors are replies, not packet loss. No local-link or external-target measurement is supplied by this adapter, and no internet, security or coverage verdict is made.",
		}}
	binding, _, err := h.enrolledGatewayBinding(ctx)
	if errors.Is(err, localapi.ErrReadTargetNotFound) {
		return out, nil
	}
	if err != nil {
		return api.QualityDiagnosis{}, err
	}
	reader, ok := h.store.(qualityHistoryReader)
	if !ok {
		return api.QualityDiagnosis{}, storage.ErrQualityHistory
	}
	page, err := reader.ReadQualityHistory(ctx, binding.ScopeID, readAt)
	if errors.Is(err, storage.ErrGatewayHistoryNotFound) || errors.Is(err, storage.ErrResolverHistoryNotFound) {
		return api.QualityDiagnosis{}, localapi.ErrReadTargetNotFound
	}
	if err != nil {
		return api.QualityDiagnosis{}, err
	}
	current, _, err := h.enrolledGatewayBinding(ctx)
	if err != nil {
		return api.QualityDiagnosis{}, err
	}
	a, b := slices.Clone(binding.Prefixes), slices.Clone(current.Prefixes)
	slices.Sort(a)
	slices.Sort(b)
	if binding.ScopeID != current.ScopeID || binding.InterfaceName != current.InterfaceName || binding.InterfaceIndex != current.InterfaceIndex || !slices.Equal(a, b) {
		return api.QualityDiagnosis{}, localapi.ErrReadTargetNotFound
	}
	if err := ctx.Err(); err != nil {
		return api.QualityDiagnosis{}, err
	}
	out.Enrolled, out.ScopeID = true, binding.ScopeID
	return diagnoseRetainedQuality(out, page)
}

func diagnoseRetainedQuality(out api.QualityDiagnosis, page storage.QualityHistoryPage) (api.QualityDiagnosis, error) {
	invalid := func() (api.QualityDiagnosis, error) { return api.QualityDiagnosis{}, storage.ErrQualityHistory }
	if len(page.Gateway.Runs) > storage.MaxGatewayHistoryRuns || len(page.Resolver.Runs) > storage.MaxResolverHistoryRuns {
		return invalid()
	}
	out.Truncated = page.Gateway.Truncated || page.Resolver.Truncated
	out.ScanTruncated = page.Gateway.ScanTruncated || page.Resolver.ScanTruncated
	unknown := func(code, summary, next string) (api.QualityDiagnosis, error) {
		out.Conclusion, out.Confidence, out.Summary, out.NextStep = code, "unknown", summary, next
		return out, nil
	}
	var g *gatewayrun.RetainedRun
	var d *resolverrun.RetainedRun
	seen := map[string]bool{}
	for i := range page.Gateway.Runs {
		r := &page.Gateway.Runs[i]
		if !gatewayrun.ValidRunID(r.RunID) || seen["gateway."+r.RunID] || r.ScopeID != out.ScopeID || !webHistoryTime(r.LastAuditAt) || r.LastAuditAt.Before(out.Since) || r.LastAuditAt.After(out.ReadAt) {
			return invalid()
		}
		seen["gateway."+r.RunID] = true
		if g == nil || r.LastAuditAt.After(g.LastAuditAt) {
			g = r
		}
	}
	for i := range page.Resolver.Runs {
		r := &page.Resolver.Runs[i]
		if !resolverrun.ValidRunID(r.RunID) || seen["resolver."+r.RunID] || r.Observer.ScopeID != out.ScopeID || !webHistoryTime(r.LastAuditAt) || r.LastAuditAt.Before(out.Since) || r.LastAuditAt.After(out.ReadAt) {
			return invalid()
		}
		seen["resolver."+r.RunID] = true
		if d == nil || r.LastAuditAt.After(d.LastAuditAt) {
			d = r
		}
	}
	// Tied latest runs have no authoritative ordering, regardless of input order.
	if g != nil {
		for _, r := range page.Gateway.Runs {
			if r.RunID != g.RunID && r.LastAuditAt.Equal(g.LastAuditAt) {
				return invalid()
			}
		}
	}
	if d != nil {
		for _, r := range page.Resolver.Runs {
			if r.RunID != d.RunID && r.LastAuditAt.Equal(d.LastAuditAt) {
				return invalid()
			}
		}
	}
	var anchor time.Time
	if g != nil {
		item := api.QualityDiagnosisRun{QualityDiagnosisReference: api.QualityDiagnosisReference{Kind: "gateway", RunID: g.RunID}, InterfaceName: g.InterfaceName, InterfaceIndex: g.InterfaceIndex, LastAuditAt: g.LastAuditAt, ExecutionOutcome: g.Outcome, SampleStatus: "no-measurement"}
		if !g.TerminalRetained {
			item.SampleStatus = "missing-terminal"
		} else if m := g.Measurement; m != nil {
			item.SampleStatus = "incomplete"
			start := m.StartedAt
			item.StartedAt = &start
			if m.CompletedAt != nil {
				end := *m.CompletedAt
				item.CompletedAt = &end
			}
			if m.Complete {
				item.SampleStatus = "recorded"
			}
		}
		out.Selected = append(out.Selected, item)
		anchor = g.LastAuditAt
	}
	if d != nil {
		item := api.QualityDiagnosisRun{QualityDiagnosisReference: api.QualityDiagnosisReference{Kind: "resolver", RunID: d.RunID}, InterfaceName: d.Observer.InterfaceName, InterfaceIndex: d.Observer.InterfaceIndex, LastAuditAt: d.LastAuditAt, ExecutionOutcome: d.Outcome, SampleStatus: "no-measurement",
			Selection: &api.ResolverHistorySelection{ID: d.Selection.ID, ResolverID: d.Selection.ResolverID, QueryID: d.Selection.QueryID, Family: string(d.Selection.Family), Transport: string(d.Selection.Transport), QueryType: string(d.Selection.QueryType), Expect: string(d.Selection.Expect)}}
		if !d.TerminalRetained {
			item.SampleStatus = "missing-terminal"
		} else if m := d.Measurement; m != nil {
			start, end := m.StartedAt, m.CompletedAt
			item.StartedAt, item.CompletedAt = &start, &end
			item.SampleStatus = "incomplete"
			if m.Exchange == nq.DNSResponseReceived || m.Exchange == nq.DNSTimeout || m.Exchange == nq.DNSTransportError {
				item.SampleStatus = "recorded"
			}
		}
		out.Selected = append(out.Selected, item)
		if d.LastAuditAt.After(anchor) {
			anchor = d.LastAuditAt
		}
	}
	if !anchor.IsZero() {
		out.AssessmentAt = &anchor
	}
	if out.Truncated || out.ScanTruncated {
		return unknown("history-incomplete", "The bounded history read is incomplete; no comparison is made.", "Inspect individual retained runs and the history limits. Missing history does not prove that no traffic was sent.")
	}
	if g == nil || d == nil {
		return unknown("insufficient-evidence", "Both a retained gateway run and resolver run are needed for this comparison.", "Inspect the available history. Any new check requires separate native approval.")
	}
	for _, r := range out.Selected {
		if r.SampleStatus != "recorded" {
			return unknown("latest-run-unmeasured", "At least one latest retained run has no complete usable sample for comparison.", "Inspect its execution outcome and retained phases. An older success cannot replace a newer missing or incomplete result.")
		}
	}
	if g.InterfaceName != d.Observer.InterfaceName || g.InterfaceIndex != d.Observer.InterfaceIndex || d.Selection.Family != nq.FamilyIPv4 {
		return unknown("observation-context-mismatch", "The latest runs do not describe the same interface and transport family.", "Read each run in its original observation context. Do not combine different interfaces or borrow IPv4 evidence for IPv6.")
	}
	if gatewayrun.ValidateMeasurement(*g.Measurement, g.LastAuditAt) != nil {
		return invalid()
	}
	// The storage reader already validates retained events. Recheck the resolver
	// terminal before translating it into the comparison contract.
	e := resolverrun.Event{SchemaVersion: d.SchemaVersion, RunID: d.RunID, State: "finished", Outcome: d.Outcome, Reason: d.Reason, At: d.LastAuditAt, Profile: d.Profile, Selection: d.Selection, Observer: d.Observer, Measurement: d.Measurement}
	if resolverrun.ValidateEvent(e) != nil {
		return invalid()
	}
	observer := nq.Observer{ScopeID: out.ScopeID, SensorID: "gateway-run-audit", InterfaceName: g.InterfaceName, InterfaceIndex: g.InterfaceIndex}
	m := g.Measurement
	start := m.StartedAt
	if d.Measurement.StartedAt.Before(start) {
		start = d.Measurement.StartedAt
	}
	if anchor.Sub(start) > nq.MaxWindow {
		return unknown("observations-too-far-apart", "The latest runs are outside the supported comparison window.", "Inspect the original sample times rather than replacing them with read time.")
	}
	outcome := nq.OutcomeSucceeded
	if m.Replies == 0 {
		outcome = nq.OutcomeFailed
	} else if m.Replies < m.AcceptedRequests {
		outcome = nq.OutcomePartial
	}
	gid, did := "gateway."+g.RunID, "resolver."+d.RunID
	in := nq.CorroborationInput{NetworkDeviceID: "local-controller-audits", ResolverDeviceID: "local-controller-audits", Family: nq.FamilyIPv4, MaxCompletionSkew: 30 * time.Second,
		Network: nq.Snapshot{Observer: observer, AsOf: anchor, WindowStart: start, Freshness: 30 * time.Second, Targets: []nq.Target{{ID: gid, Layer: nq.LayerGateway, Method: nq.MethodICMP, Family: nq.FamilyIPv4}},
			Measurements: []nq.Measurement{{ID: gid, TargetID: gid, Observer: observer, StartedAt: m.StartedAt, CompletedAt: *m.CompletedAt, Outcome: outcome, Attempts: m.AcceptedRequests, Successes: m.Replies}}},
		Resolvers: nq.ResolverSnapshot{Observer: d.Observer, AsOf: anchor, WindowStart: start, Freshness: 30 * time.Second, Selections: []nq.ResolverSelection{d.Selection}}}
	dm := d.Measurement
	sample := nq.ResolverMeasurement{ID: did, Selection: d.Selection, Observer: d.Observer, StartedAt: dm.StartedAt, CompletedAt: dm.CompletedAt, Exchange: dm.Exchange, Request: dm.Request, Gap: dm.Gap, Reply: dm.Reply}
	if dm.ResponseTimeNanoseconds != nil {
		duration := time.Duration(*dm.ResponseTimeNanoseconds)
		sample.ResponseTime = &duration
	}
	in.Resolvers.Measurements = []nq.ResolverMeasurement{sample}
	result, err := nq.Corroborate(in)
	if err != nil {
		return invalid()
	}
	out.Conclusion, out.Confidence, out.Summary, out.NextStep = result.Conclusion, string(result.Confidence), "Historical comparison: "+result.Summary, result.NextStep
	out.Limitations = append(out.Limitations, result.Limitations...)
	for _, id := range result.Compared {
		switch id {
		case gid:
			out.Compared = append(out.Compared, api.QualityDiagnosisReference{Kind: "gateway", RunID: g.RunID})
		case did:
			out.Compared = append(out.Compared, api.QualityDiagnosisReference{Kind: "resolver", RunID: d.RunID})
		default:
			return invalid()
		}
	}
	if len(out.Compared) > 0 {
		start, end := result.EvidenceStart, result.EvidenceEnd
		out.EvidenceStart, out.EvidenceEnd = &start, &end
	}
	return out, nil
}
