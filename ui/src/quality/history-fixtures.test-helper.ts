import type { GatewayHistory, HistoryEvidence, HistoryRun } from "./gateway-history";
// Synthetic retained records. No network checks are executed to create fixtures.
export function historyFixture(evidence: HistoryEvidence = "all-replied"): GatewayHistory {
  const run: HistoryRun = { run_id: "a".repeat(32), interface_name: "en0", interface_index: 7, target: "192.168.50.1", source: "192.168.50.23",
    last_audit_at: "2026-09-12T11:59:59Z", authorization_retained: true, admission_retained: true, terminal_retained: true,
    outcome: "completed", evidence, confidence: "limited", measurement: { started_at: "2026-09-12T11:59:56Z", completed_at: "2026-09-12T11:59:59Z",
      send_calls: 3, accepted_requests: 3, replies: 3, timeouts: 0, complete: true, mean_rtt_ns: 0 } };
  if (evidence === "missing-terminal" || evidence === "execution-only" || evidence === "no-measurement") {
    delete run.measurement; run.confidence = "unknown";
    if (evidence === "missing-terminal") { run.terminal_retained = false; run.outcome = "unknown"; }
    if (evidence === "no-measurement") run.outcome = "failed";
  } else if (evidence === "incomplete") {
    run.confidence = "unknown"; run.outcome = "canceled";
    run.measurement = { started_at: "2026-09-12T11:59:56Z", send_calls: 1, accepted_requests: 1, replies: 0, timeouts: 0, complete: false };
  } else if (evidence !== "all-replied") {
    run.measurement!.replies = evidence === "no-replies" ? 0 : 1;
    run.measurement!.timeouts = 3 - run.measurement!.replies;
    if (evidence === "no-replies") delete run.measurement!.mean_rtt_ns;
  }
  return { enrolled: true, as_of: "2026-09-12T12:00:00Z", since: "2026-09-11T12:00:00Z", truncated: false, scan_truncated: false, runs: [run] };
}
