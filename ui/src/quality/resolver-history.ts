export type ResolverEvidence = "unknown" | "not-measured" | "incomplete" | "timeout" | "transport-failure" | "answer" | "nxdomain" | "no-data" | "refused" | "server-failure" | "format-error" | "not-implemented" | "other-response-error" | "referral" | "truncated" | "unclassified-response";
export type ResolverOutcome = "unknown" | "completed" | "blocked" | "canceled" | "failed" | "indeterminate";
export interface ResolverMeasurement {
  started_at: string; completed_at: string; exchange: "response-received" | "timeout" | "transport-error" | "incomplete" | "not-measured";
  request: "not-sent" | "accepted" | "uncertain"; rcode?: number; response_time_ns?: number;
}
export interface ResolverHistoryRun {
  run_id: string; selection: { id: string; resolver_id: string; query_id: string; family: "ipv4" | "ipv6"; transport: "udp"; query_type: "A" | "AAAA"; expect: "answer" | "nxdomain" | "no-data" };
  interface_name: string; interface_index: number; last_audit_at: string;
  authorization_retained: boolean; admission_retained: boolean; terminal_retained: boolean;
  outcome: ResolverOutcome; evidence: ResolverEvidence; confidence: "unknown" | "limited";
  expectation_matched?: boolean; measurement?: ResolverMeasurement;
}
export interface ResolverHistory {
  enrolled: boolean; as_of: string; since: string; truncated: boolean; scan_truncated: boolean; runs: ResolverHistoryRun[];
}
export type ResolverHistoryLoader = (signal: AbortSignal) => Promise<ResolverHistory>;
function invalid(): never { throw new Error("Retained resolver history is invalid."); }
function object(value: unknown, required: string[], optional: string[] = []): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return invalid();
  const v = value as Record<string, unknown>;
  if (required.some((key) => !Object.hasOwn(v, key)) || Object.keys(v).some((key) => !required.includes(key) && !optional.includes(key))) return invalid();
  return v;
}
function timestamp(value: unknown): string {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(value) ||
    !Number.isFinite(Date.parse(value)) || new Date(value).toISOString().slice(0, 19) !== value.slice(0, 19)) return invalid();
  const ns = nanos(value);
  if (ns < -9223372036854775808n || ns > 9223372036854775807n) return invalid();
  return value;
}
function nanos(value: string): bigint {
  return BigInt(Date.parse(value.slice(0, 19) + "Z")) * 1000000n + BigInt((value.split(".")[1]?.slice(0, -1) ?? "").padEnd(9, "0"));
}
function bool(value: unknown): boolean { return typeof value === "boolean" ? value : invalid(); }
function integer(value: unknown, max: number): number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0 && value <= max ? value : invalid();
}
function choice<T extends string>(value: unknown, choices: readonly T[]): T {
  return typeof value === "string" && choices.includes(value as T) ? value as T : invalid();
}
function reference(value: unknown): string {
  return typeof value === "string" && value.length <= 128 && /^[a-z0-9]+(?:[._-][a-z0-9]+)*$/.test(value) ? value : invalid();
}
function measurement(raw: unknown, audit: string): ResolverMeasurement {
  const v = object(raw, ["started_at", "completed_at", "exchange", "request"], ["rcode", "response_time_ns"]);
  const m: ResolverMeasurement = { started_at: timestamp(v.started_at), completed_at: timestamp(v.completed_at),
    exchange: choice(v.exchange, ["response-received", "timeout", "transport-error", "incomplete", "not-measured"]),
    request: choice(v.request, ["not-sent", "accepted", "uncertain"]) };
  if (Object.hasOwn(v, "rcode")) m.rcode = integer(v.rcode, 15);
  if (Object.hasOwn(v, "response_time_ns")) m.response_time_ns = integer(v.response_time_ns, 1999999999);
  const duration = nanos(m.completed_at) - nanos(m.started_at);
  if (duration < 0n || duration >= 5000000000n || nanos(m.completed_at) > nanos(audit) ||
    (m.response_time_ns !== undefined && BigInt(m.response_time_ns) > duration)) return invalid();
  if (m.exchange === "response-received") {
    if (m.request !== "accepted" || m.rcode === undefined || m.response_time_ns === undefined) return invalid();
  } else if (m.rcode !== undefined || m.response_time_ns !== undefined ||
    (m.exchange === "timeout" && (m.request !== "accepted" || duration < 2000000000n)) ||
    (m.exchange === "not-measured" && m.request !== "not-sent")) return invalid();
  return m;
}
export function parseResolverHistory(raw: unknown): ResolverHistory {
  const v = object(raw, ["enrolled", "as_of", "since", "truncated", "scan_truncated", "runs"]);
  const out: ResolverHistory = { enrolled: bool(v.enrolled), as_of: timestamp(v.as_of), since: timestamp(v.since), truncated: bool(v.truncated), scan_truncated: bool(v.scan_truncated), runs: [] };
  if (nanos(out.as_of) - nanos(out.since) !== 86400000000000n || !Array.isArray(v.runs) || v.runs.length > 20 ||
    (!out.enrolled && (v.runs.length !== 0 || out.truncated || out.scan_truncated))) return invalid();
  const seen = new Set<string>();
  for (const rawRun of v.runs) {
    const r = object(rawRun, ["run_id", "selection", "interface_name", "interface_index", "last_audit_at", "authorization_retained", "admission_retained", "terminal_retained", "outcome", "evidence", "confidence"], ["measurement", "expectation_matched"]);
    const s = object(r.selection, ["id", "resolver_id", "query_id", "family", "transport", "query_type", "expect"]);
    if (typeof r.run_id !== "string" || !/^[0-9a-f]{32}$/.test(r.run_id) || seen.has(r.run_id) ||
      typeof r.interface_name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(r.interface_name)) return invalid();
    seen.add(r.run_id);
    const run: ResolverHistoryRun = { run_id: r.run_id, interface_name: r.interface_name, interface_index: integer(r.interface_index, 2147483647),
      selection: { id: reference(s.id), resolver_id: reference(s.resolver_id), query_id: reference(s.query_id), family: choice(s.family, ["ipv4", "ipv6"]), transport: choice(s.transport, ["udp"]), query_type: choice(s.query_type, ["A", "AAAA"]), expect: choice(s.expect, ["answer", "nxdomain", "no-data"]) },
      last_audit_at: timestamp(r.last_audit_at), authorization_retained: bool(r.authorization_retained), admission_retained: bool(r.admission_retained), terminal_retained: bool(r.terminal_retained),
      outcome: choice(r.outcome, ["unknown", "completed", "blocked", "canceled", "failed", "indeterminate"]),
      evidence: choice(r.evidence, ["unknown", "not-measured", "incomplete", "timeout", "transport-failure", "answer", "nxdomain", "no-data", "refused", "server-failure", "format-error", "not-implemented", "other-response-error", "referral", "truncated", "unclassified-response"]),
      confidence: choice(r.confidence, ["unknown", "limited"]) };
    if (run.interface_index === 0 || nanos(run.last_audit_at) < nanos(out.since) || nanos(run.last_audit_at) > nanos(out.as_of) ||
      (!run.authorization_retained && !run.admission_retained && !run.terminal_retained)) return invalid();
    if (Object.hasOwn(r, "measurement")) run.measurement = measurement(r.measurement, run.last_audit_at);
    if (Object.hasOwn(r, "expectation_matched")) run.expectation_matched = bool(r.expectation_matched);
    const m = run.measurement, evidence = run.evidence;
    if (!run.terminal_retained && (run.outcome !== "unknown" || m !== undefined)) return invalid();
    if (run.terminal_retained && run.outcome === "unknown") return invalid();
    if (m === undefined) {
      if (evidence !== "unknown" || run.outcome === "completed") return invalid();
    } else {
      if (!["completed", "failed", "canceled"].includes(run.outcome)) return invalid();
      if (run.outcome === "completed" && m.exchange !== "response-received" && m.exchange !== "timeout") return invalid();
      if (m.exchange === "response-received") {
        const codes: Record<string, number> = { answer: 0, "no-data": 0, referral: 0, "unclassified-response": 0, "format-error": 1, "server-failure": 2, nxdomain: 3, "not-implemented": 4, refused: 5 };
        if (evidence !== "truncated" && (evidence === "other-response-error" ? m.rcode! < 6 : codes[evidence] === undefined || codes[evidence] !== m.rcode)) return invalid();
      } else {
        const states = { timeout: "timeout", "transport-error": "transport-failure", incomplete: "incomplete", "not-measured": "not-measured" };
        if (evidence !== states[m.exchange]) return invalid();
      }
    }
    if (run.confidence !== (["unknown", "not-measured", "incomplete"].includes(evidence) ? "unknown" : "limited")) return invalid();
    if (["answer", "nxdomain", "no-data"].includes(evidence)) {
      if (run.expectation_matched !== (evidence === run.selection.expect)) return invalid();
    } else if (run.expectation_matched !== undefined) return invalid();
    out.runs.push(run);
  }
  return out;
}

export const loadResolverHistoryFromWeb: ResolverHistoryLoader = async (signal) => {
  const response = await fetch("/api/network-quality/resolver-history", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal });
  if (!response.ok || response.headers.get("Content-Type")?.split(";")[0]?.trim().toLowerCase() !== "application/json" || !response.body) {
    await response.body?.cancel();
    throw new Error("Retained resolver history is unavailable.");
  }
  const reader = response.body.getReader(), decoder = new TextDecoder("utf-8", { fatal: true });
  let bytes = 0, text = "";
  try {
    for (;;) {
      const chunk = await reader.read();
      if (chunk.done) break;
      bytes += chunk.value.byteLength;
      if (bytes > 65536) invalid();
      text += decoder.decode(chunk.value, { stream: true });
    }
    text += decoder.decode();
    return parseResolverHistory(JSON.parse(text) as unknown);
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
};
