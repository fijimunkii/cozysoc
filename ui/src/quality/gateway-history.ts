export type HistoryEvidence = "missing-terminal" | "execution-only" | "no-measurement" | "incomplete" | "all-replied" | "some-replies" | "no-replies";
export type HistoryOutcome = "unknown" | "completed" | "blocked" | "canceled" | "failed" | "indeterminate";
export interface HistoryMeasurement {
  started_at: string; completed_at?: string; send_calls: number; accepted_requests: number;
  replies: number; timeouts: number; complete: boolean; mean_rtt_ns?: number;
}
export interface HistoryRun {
  run_id: string; interface_name: string; interface_index: number; target: string; source: string;
  last_audit_at: string; authorization_retained: boolean; admission_retained: boolean; terminal_retained: boolean;
  outcome: HistoryOutcome; evidence: HistoryEvidence; confidence: "unknown" | "limited"; measurement?: HistoryMeasurement;
}
export interface GatewayHistory {
  enrolled: boolean; as_of: string; since: string; truncated: boolean; scan_truncated: boolean; runs: HistoryRun[];
}
export type GatewayHistoryLoader = (signal: AbortSignal) => Promise<GatewayHistory>;
function invalid(): never { throw new Error("Retained gateway history is invalid."); }
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
function ipv4(value: unknown): string {
  if (typeof value !== "string" || !/^(?:0|[1-9]\d{0,2})(?:\.(?:0|[1-9]\d{0,2})){3}$/.test(value)) return invalid();
  const parts = value.split(".").map(Number);
  if (parts.some((v) => v > 255) || !(parts[0] === 10 || (parts[0] === 172 && parts[1]! >= 16 && parts[1]! <= 31) || (parts[0] === 192 && parts[1] === 168))) return invalid();
  return value;
}
function measurement(raw: unknown, audit: string): HistoryMeasurement {
  const v = object(raw, ["started_at", "send_calls", "accepted_requests", "replies", "timeouts", "complete"], ["completed_at", "mean_rtt_ns"]);
  const m: HistoryMeasurement = { started_at: timestamp(v.started_at), send_calls: integer(v.send_calls, 3), accepted_requests: integer(v.accepted_requests, 3),
    replies: integer(v.replies, 3), timeouts: integer(v.timeouts, 3), complete: bool(v.complete) };
  if (Object.hasOwn(v, "completed_at")) m.completed_at = timestamp(v.completed_at);
  if (Object.hasOwn(v, "mean_rtt_ns")) m.mean_rtt_ns = integer(v.mean_rtt_ns, 999999999);
  const finished = m.replies + m.timeouts, duration = nanos(m.completed_at ?? audit) - nanos(m.started_at);
  if (m.accepted_requests > m.send_calls || m.send_calls - m.accepted_requests > 1 || finished > m.accepted_requests ||
    m.accepted_requests - finished > 1 || (m.send_calls > m.accepted_requests && finished !== m.accepted_requests) ||
    nanos(m.started_at) > nanos(audit) || duration < BigInt(Math.max(m.send_calls - 1, m.timeouts, 0)) * 1000000000n ||
    (m.completed_at !== undefined && (nanos(m.completed_at) > nanos(audit) || finished !== 3 || duration >= 5000000000n)) ||
    (m.complete && (m.send_calls !== 3 || m.accepted_requests !== 3 || finished !== 3 || m.completed_at === undefined)) ||
    (!m.complete && m.mean_rtt_ns !== undefined) || (m.complete && (m.mean_rtt_ns !== undefined) !== (m.replies > 0)) ||
    (m.mean_rtt_ns !== undefined && BigInt(m.mean_rtt_ns) > duration)) return invalid();
  return m;
}

export function parseGatewayHistory(raw: unknown): GatewayHistory {
  const v = object(raw, ["enrolled", "as_of", "since", "truncated", "scan_truncated", "runs"]);
  const result: GatewayHistory = { enrolled: bool(v.enrolled), as_of: timestamp(v.as_of), since: timestamp(v.since),
    truncated: bool(v.truncated), scan_truncated: bool(v.scan_truncated), runs: [] };
  if (nanos(result.as_of) - nanos(result.since) !== 86400000000000n || !Array.isArray(v.runs) || v.runs.length > 20 ||
    (!result.enrolled && (v.runs.length !== 0 || result.truncated || result.scan_truncated))) return invalid();
  const seen = new Set<string>();
  for (const rawRun of v.runs) {
    const r = object(rawRun, ["run_id", "interface_name", "interface_index", "target", "source", "last_audit_at", "authorization_retained", "admission_retained", "terminal_retained", "outcome", "evidence", "confidence"], ["measurement"]);
    if (typeof r.run_id !== "string" || !/^[0-9a-f]{32}$/.test(r.run_id) || seen.has(r.run_id) ||
      typeof r.interface_name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(r.interface_name)) return invalid();
    seen.add(r.run_id);
    const outcome = r.outcome, evidence = r.evidence, confidence = r.confidence;
    if (outcome !== "unknown" && outcome !== "completed" && outcome !== "blocked" && outcome !== "canceled" && outcome !== "failed" && outcome !== "indeterminate") return invalid();
    if (evidence !== "missing-terminal" && evidence !== "execution-only" && evidence !== "no-measurement" && evidence !== "incomplete" && evidence !== "all-replied" && evidence !== "some-replies" && evidence !== "no-replies") return invalid();
    if (confidence !== "unknown" && confidence !== "limited") return invalid();
    const run: HistoryRun = { run_id: r.run_id, interface_name: r.interface_name, interface_index: integer(r.interface_index, 2147483647),
      target: ipv4(r.target), source: ipv4(r.source), last_audit_at: timestamp(r.last_audit_at), authorization_retained: bool(r.authorization_retained),
      admission_retained: bool(r.admission_retained), terminal_retained: bool(r.terminal_retained), outcome, evidence, confidence };
    if (run.interface_index === 0 || run.source === run.target || nanos(run.last_audit_at) < nanos(result.since) || nanos(run.last_audit_at) > nanos(result.as_of) ||
      (!run.authorization_retained && !run.admission_retained && !run.terminal_retained)) return invalid();
    if (Object.hasOwn(r, "measurement")) run.measurement = measurement(r.measurement, run.last_audit_at);
    const m = run.measurement;
    let expected: HistoryEvidence = "missing-terminal";
    if (!run.terminal_retained) {
      if (outcome !== "unknown" || m !== undefined) return invalid();
    } else {
      if (outcome === "unknown") return invalid();
      expected = evidence === "execution-only" ? "execution-only" : "no-measurement";
      if (m !== undefined) {
        if (evidence === "execution-only" || (outcome !== "completed" && outcome !== "failed" && outcome !== "canceled") || (outcome === "failed" && m.complete)) return invalid();
        expected = m.complete ? m.replies === 3 ? "all-replied" : m.replies === 0 ? "no-replies" : "some-replies" : "incomplete";
      }
      if (outcome === "completed" && evidence !== "execution-only" && !m?.complete) return invalid();
    }
    if (evidence !== expected || confidence !== (m?.complete ? "limited" : "unknown")) return invalid();
    result.runs.push(run);
  }
  return result;
}

export const loadGatewayHistoryFromWeb: GatewayHistoryLoader = async (signal) => {
  const response = await fetch("/api/network-quality/history", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal });
  if (!response.ok || response.headers.get("Content-Type")?.split(";")[0]?.trim().toLowerCase() !== "application/json" || !response.body) {
    await response.body?.cancel();
    throw new Error("Retained gateway history is unavailable.");
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
    return parseGatewayHistory(JSON.parse(text) as unknown);
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
};
