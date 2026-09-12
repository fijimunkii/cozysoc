import type { HTTPSHistory, HTTPSEvidence } from "./https-history";
export function httpsHistoryFixture(evidence: HTTPSEvidence = "status-response"): HTTPSHistory {
 const value: HTTPSHistory = { enrolled: true, as_of: "2026-09-12T12:00:00Z", since: "2026-09-11T12:00:00Z", truncated: false, scan_truncated: false,
  runs: [{ run_id: "a".repeat(32), selection: { id: "selection.test", endpoint_id: "endpoint.test", request_id: "request.test", family: "ipv4", method: "HEAD", expected_status: 204 },
   interface_name: "en0", interface_index: 7, last_audit_at: "2026-09-12T11:59:59Z", authorization_retained: true, admission_retained: true, terminal_retained: true,
   outcome: "completed", evidence, confidence: "limited", measurement: { started_at: "2026-09-12T11:59:56Z", completed_at: "2026-09-12T11:59:59Z", exchange: "response-received", stage: "request", request: "accepted", status_code: 204, response_time_ns: 0 } }] };
 const r = value.runs[0]!, m = r.measurement!;
 if (evidence === "unknown") { r.terminal_retained = false; r.outcome = "unknown"; r.confidence = "unknown"; delete r.measurement; }
 else if (evidence === "status-response" || evidence === "redirect-response") { m.status_code = evidence === "redirect-response" ? 301 : 204; r.expectation_matched = m.status_code === r.selection.expected_status; }
 else {
  const exchanges = { "connection-failure": "connect-error", "tls-failure": "tls-error", "transport-failure": "transport-error", "protocol-failure": "protocol-error", timeout: "timeout", incomplete: "incomplete", "not-measured": "not-measured" } as const;
  m.exchange = exchanges[evidence]; delete m.status_code; delete m.response_time_ns;
  if (evidence !== "timeout") r.outcome = "failed";
  if (evidence === "incomplete" || evidence === "not-measured") r.confidence = "unknown";
  if (evidence === "connection-failure" || evidence === "tls-failure" || evidence === "not-measured") { m.stage = evidence === "connection-failure" ? "connect" : evidence === "tls-failure" ? "tls" : ""; m.request = "not-sent"; }
 }
 return value;
}
