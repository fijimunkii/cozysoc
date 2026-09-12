import { parseHTTPSHistory, type HTTPSHistoryRun } from "./https-history";
import type { ResolverHistoryRun, ResolverOutcome } from "./resolver-history";
export type DiagnosisConclusion = "external-check-issue-with-responses" | "not-enrolled" | "history-incomplete" | "insufficient-evidence" | "latest-run-unmeasured" | "observation-context-mismatch" | "observations-too-far-apart" | "dns-query-issue-with-responses" | "icmp-misses-with-responses" | "problems-across-selected-layers" | "selected-checks-matched" | "mixed-or-limited-evidence";
export interface DiagnosisReference { kind: "gateway" | "resolver" | "https"; run_id: string }
export interface DiagnosisRun extends DiagnosisReference {
 https?: HTTPSHistoryRun;
  interface_name: string; interface_index: number; last_audit_at: string; execution_outcome: ResolverOutcome;
  sample_status: "missing-terminal" | "no-measurement" | "incomplete" | "recorded";
  started_at?: string; completed_at?: string; selection?: ResolverHistoryRun["selection"];
}
export interface QualityDiagnosis {
  enrolled: boolean; read_at: string; since: string; truncated: boolean; scan_truncated: boolean; assessment_at?: string;
  selected: DiagnosisRun[]; compared: DiagnosisReference[]; evidence_start?: string; evidence_end?: string;
  conclusion: DiagnosisConclusion; confidence: "unknown" | "limited";
}
export type QualityDiagnosisLoader = (signal: AbortSignal) => Promise<QualityDiagnosis>;
function invalid(): never { throw new Error("Historical diagnosis is invalid."); }
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
function runReference(raw: unknown): DiagnosisReference {
  const v = object(raw, ["kind", "run_id"]);
  if (typeof v.run_id !== "string" || !/^[0-9a-f]{32}$/.test(v.run_id)) return invalid();
  return { kind: choice(v.kind, ["gateway", "resolver", "https"]), run_id: v.run_id };
}
export function parseQualityDiagnosis(raw: unknown): QualityDiagnosis {
  const v = object(raw, ["enrolled", "read_at", "since", "truncated", "scan_truncated", "selected", "compared", "conclusion", "confidence"], ["assessment_at", "evidence_start", "evidence_end"]);
  const out: QualityDiagnosis = { enrolled: bool(v.enrolled), read_at: timestamp(v.read_at), since: timestamp(v.since), truncated: bool(v.truncated), scan_truncated: bool(v.scan_truncated), selected: [], compared: [],
    conclusion: choice(v.conclusion, ["not-enrolled", "history-incomplete", "insufficient-evidence", "latest-run-unmeasured", "observation-context-mismatch", "observations-too-far-apart", "dns-query-issue-with-responses", "icmp-misses-with-responses", "problems-across-selected-layers", "selected-checks-matched", "mixed-or-limited-evidence", "external-check-issue-with-responses"]), confidence: choice(v.confidence, ["unknown", "limited"]) };
  for (const key of ["assessment_at", "evidence_start", "evidence_end"] as const) if (Object.hasOwn(v, key)) out[key] = timestamp(v[key]);
  if (nanos(out.read_at) - nanos(out.since) !== 86400000000000n || !Array.isArray(v.selected) || v.selected.length > 3 || !Array.isArray(v.compared) || v.compared.length > 3 ||
    (!out.enrolled && (v.selected.length > 0 || out.truncated || out.scan_truncated))) return invalid();
  const selected = new Map<string, DiagnosisRun>();
  let anchor: string | undefined, start: string | undefined, end: string | undefined;
  for (const rawRun of v.selected) {
    const r = object(rawRun, ["kind", "run_id", "interface_name", "interface_index", "last_audit_at", "execution_outcome", "sample_status"], ["started_at", "completed_at", "selection", "https"]);
    const ref = runReference({ kind: r.kind, run_id: r.run_id });
    if (selected.has(ref.kind) || typeof r.interface_name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(r.interface_name)) return invalid();
    const run: DiagnosisRun = { ...ref, interface_name: r.interface_name, interface_index: integer(r.interface_index, 2147483647), last_audit_at: timestamp(r.last_audit_at),
      execution_outcome: choice(r.execution_outcome, ["unknown", "completed", "blocked", "canceled", "failed", "indeterminate"]), sample_status: choice(r.sample_status, ["missing-terminal", "no-measurement", "incomplete", "recorded"]) };
    for (const key of ["started_at", "completed_at"] as const) if (Object.hasOwn(r, key)) run[key] = timestamp(r[key]);
    if (run.interface_index === 0 || nanos(run.last_audit_at) < nanos(out.since) || nanos(run.last_audit_at) > nanos(out.read_at) || (run.sample_status === "missing-terminal") !== (run.execution_outcome === "unknown")) return invalid();
    if (run.sample_status === "missing-terminal" || run.sample_status === "no-measurement") {
      if (run.started_at !== undefined || run.completed_at !== undefined) return invalid();
    } else {
      if (run.started_at === undefined || nanos(run.started_at) > nanos(run.last_audit_at) || !["completed", "failed", "canceled"].includes(run.execution_outcome) || (run.sample_status === "incomplete" && run.execution_outcome === "completed")) return invalid();
      if (run.completed_at !== undefined && (nanos(run.completed_at) < nanos(run.started_at) || nanos(run.completed_at) > nanos(run.last_audit_at) || (run.kind !== "https" && nanos(run.completed_at) - nanos(run.started_at) >= 5000000000n))) return invalid();
      if ((run.sample_status === "recorded" || run.kind !== "gateway") && run.completed_at === undefined) return invalid();
      if (run.kind === "gateway" && run.sample_status === "recorded" && (run.execution_outcome === "failed" || nanos(run.completed_at!) - nanos(run.started_at) < 2000000000n)) return invalid();
    }
    if (run.kind === "resolver") {
      const s = object(r.selection, ["id", "resolver_id", "query_id", "family", "transport", "query_type", "expect"]);
      run.selection = { id: reference(s.id), resolver_id: reference(s.resolver_id), query_id: reference(s.query_id), family: choice(s.family, ["ipv4", "ipv6"]), transport: choice(s.transport, ["udp"]), query_type: choice(s.query_type, ["A", "AAAA"]), expect: choice(s.expect, ["answer", "nxdomain", "no-data"]) };
      if (run.execution_outcome === "completed" && run.sample_status !== "recorded") return invalid();
    } else if (Object.hasOwn(r, "selection")) return invalid();
    if (run.kind === "https") {
      const h = parseHTTPSHistory({ enrolled: out.enrolled, as_of: out.read_at, since: out.since, truncated: false, scan_truncated: false, runs: [r.https] }).runs[0]!;
      const status = !h.terminal_retained ? "missing-terminal" : h.measurement === undefined ? "no-measurement" : ["incomplete", "not-measured"].includes(h.measurement.exchange) ? "incomplete" : "recorded";
      if (h.run_id !== run.run_id || h.interface_name !== run.interface_name || h.interface_index !== run.interface_index || nanos(h.last_audit_at) !== nanos(run.last_audit_at) || h.outcome !== run.execution_outcome || status !== run.sample_status) return invalid();
      if (h.measurement !== undefined && (run.started_at === undefined || run.completed_at === undefined || nanos(h.measurement.started_at) !== nanos(run.started_at) || nanos(h.measurement.completed_at) !== nanos(run.completed_at))) return invalid();
      run.https = h;
    } else if (Object.hasOwn(r, "https")) return invalid();
    selected.set(run.kind, run); out.selected.push(run);
    if (anchor === undefined || nanos(run.last_audit_at) > nanos(anchor)) anchor = run.last_audit_at;
    if (run.started_at !== undefined && (start === undefined || nanos(run.started_at) < nanos(start))) start = run.started_at;
    if (run.completed_at !== undefined && (end === undefined || nanos(run.completed_at) > nanos(end))) end = run.completed_at;
  }
  if ((anchor === undefined) !== (out.assessment_at === undefined) || (anchor !== undefined && nanos(anchor) !== nanos(out.assessment_at!))) return invalid();
  const family = (r: DiagnosisRun): string => r.kind === "gateway" ? "ipv4" : r.kind === "resolver" ? r.selection!.family : r.https!.selection.family;
  const first = out.selected[0]!;
  let expected: DiagnosisConclusion | undefined;
  if (!out.enrolled) expected = "not-enrolled";
  else if (out.truncated || out.scan_truncated) expected = "history-incomplete";
  else if (selected.size < 2) expected = "insufficient-evidence";
  else if (out.selected.some((r) => r.sample_status !== "recorded")) expected = "latest-run-unmeasured";
  else if (out.selected.some((r) => r.interface_name !== first.interface_name || r.interface_index !== first.interface_index || family(r) !== family(first))) expected = "observation-context-mismatch";
  else if (nanos(anchor!) - nanos(start!) > 86400000000000n) expected = "observations-too-far-apart";
  else if (out.selected.some((r) => nanos(anchor!) - nanos(r.completed_at!) >= 30000000000n)) expected = "insufficient-evidence";
  if (expected !== undefined) {
    if (out.conclusion !== expected || out.confidence !== "unknown" || v.compared.length !== 0 || out.evidence_start !== undefined || out.evidence_end !== undefined) return invalid();
  } else {
    if ((out.conclusion === "external-check-issue-with-responses" && !selected.has("https")) || (out.conclusion === "dns-query-issue-with-responses" && !selected.has("resolver")) || (out.conclusion === "icmp-misses-with-responses" && !selected.has("gateway"))) return invalid();
    const https = selected.get("https")?.https;
    if (https !== undefined && ((out.conclusion === "selected-checks-matched" && https.expectation_matched !== true) || (out.conclusion === "external-check-issue-with-responses" && https.expectation_matched === true))) return invalid();
    if (!["dns-query-issue-with-responses", "icmp-misses-with-responses", "problems-across-selected-layers", "selected-checks-matched", "mixed-or-limited-evidence", "external-check-issue-with-responses"].includes(out.conclusion) || out.confidence !== "limited" || v.compared.length !== out.selected.length || out.evidence_start === undefined || out.evidence_end === undefined || nanos(out.evidence_start) !== nanos(start!) || nanos(out.evidence_end) !== nanos(end!)) return invalid();
    const seen = new Set<string>();
    for (const ref of v.compared.map(runReference)) {
      if (seen.has(ref.kind) || selected.get(ref.kind)?.run_id !== ref.run_id) return invalid();
      seen.add(ref.kind); out.compared.push(ref);
    }
  }
  return out;
}

export const loadQualityDiagnosisFromWeb: QualityDiagnosisLoader = async (signal) => {
  const response = await fetch("/api/network-quality/diagnosis", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal });
  if (!response.ok || response.headers.get("Content-Type")?.split(";")[0]?.trim().toLowerCase() !== "application/json" || !response.body) {
    await response.body?.cancel();
    throw new Error("Historical diagnosis is unavailable.");
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
    return parseQualityDiagnosis(JSON.parse(text) as unknown);
  } finally {
    await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
};
