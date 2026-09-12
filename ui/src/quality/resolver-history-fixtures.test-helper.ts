import type { ResolverHistory, ResolverEvidence } from "./resolver-history";
export function resolverHistoryFixture(evidence: ResolverEvidence = "answer"): ResolverHistory {
  const value: ResolverHistory = { enrolled: true, as_of: "2026-09-12T12:00:00Z", since: "2026-09-11T12:00:00Z", truncated: false, scan_truncated: false,
    runs: [{ run_id: "a".repeat(32), selection: { id: "selection.test", resolver_id: "resolver.test", query_id: "query.test", family: "ipv4", transport: "udp", query_type: "AAAA", expect: "answer" },
      interface_name: "en0", interface_index: 7, last_audit_at: "2026-09-12T11:59:59Z", authorization_retained: true, admission_retained: true, terminal_retained: true,
      outcome: "completed", evidence, confidence: "limited", measurement: { started_at: "2026-09-12T11:59:56Z", completed_at: "2026-09-12T11:59:59Z", exchange: "response-received", request: "accepted", rcode: 0, response_time_ns: 0 } }] };
  const r = value.runs[0]!, m = r.measurement!;
  if (evidence === "unknown") { r.terminal_retained = false; r.outcome = "unknown"; r.confidence = "unknown"; delete r.measurement; }
  else if (["timeout", "transport-failure", "incomplete", "not-measured"].includes(evidence)) {
    m.exchange = evidence === "transport-failure" ? "transport-error" : evidence as "timeout" | "incomplete" | "not-measured";
    delete m.rcode; delete m.response_time_ns;
    if (evidence !== "timeout") r.outcome = "failed";
    if (evidence === "incomplete" || evidence === "not-measured") r.confidence = "unknown";
    if (evidence === "not-measured") m.request = "not-sent";
  } else {
    m.rcode = ({ "format-error": 1, "server-failure": 2, nxdomain: 3, "not-implemented": 4, refused: 5, "other-response-error": 6 } as Record<string, number>)[evidence] ?? 0;
    if (["answer", "nxdomain", "no-data"].includes(evidence)) r.expectation_matched = evidence === r.selection.expect;
  }
  return value;
}
