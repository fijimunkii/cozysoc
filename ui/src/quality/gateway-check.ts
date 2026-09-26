export interface GatewayCheckBudget {
  max_attempts: number;
  min_interval_ms: number;
  attempt_timeout_ms: number;
  total_timeout_ms: number;
  payload_bytes: number;
  max_icmp_request_bytes: number;
  max_concurrent_runs: number;
  min_run_interval_ms: number;
}

export interface GatewayCheckReview {
  review_id: string;
  created_at: string;
  expires_at: string;
  target: string;
  source: string;
  interface_name: string;
  interface_index: number;
  prefixes: string[];
  budget: GatewayCheckBudget;
}

export interface GatewayCheckResult {
  outcome: "declined" | "completed" | "blocked" | "canceled" | "failed" | "indeterminate";
  run_id?: string;
  failure_code?: string;
  measurement?: {
    started_at: string;
    completed_at?: string;
    send_calls: number;
    accepted_requests: number;
    replies: number;
    timeouts: number;
    complete: boolean;
    mean_rtt_ns?: number;
  };
}

export interface GatewayCheckClient {
  reviewGateway(target: string): Promise<GatewayCheckReview>;
  decideGateway(reviewID: string, approve: boolean): Promise<GatewayCheckResult>;
}

function invalid(): never { throw new Error("Gateway check response is invalid."); }
function object(raw: unknown): Record<string, unknown> {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return invalid();
  return raw as Record<string, unknown>;
}
function exact(raw: unknown, required: string[], optional: string[] = []): Record<string, unknown> {
  const value = object(raw);
  if (required.some((key) => !Object.hasOwn(value, key)) || Object.keys(value).some((key) => !required.includes(key) && !optional.includes(key))) return invalid();
  return value;
}
function uint(raw: unknown, max: number): number {
  if (typeof raw !== "number" || !Number.isSafeInteger(raw) || raw < 0 || raw > max) return invalid();
  return raw;
}
function time(raw: unknown): string {
  if (typeof raw !== "string" || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(raw) || !Number.isFinite(Date.parse(raw))) return invalid();
  return raw;
}
function privateIPv4(raw: unknown): string {
  if (typeof raw !== "string" || !/^(?:0|[1-9]\d{0,2})(?:\.(?:0|[1-9]\d{0,2})){3}$/.test(raw)) return invalid();
  const p = raw.split(".").map(Number);
  if (p.some((n) => n > 255) || !(p[0] === 10 || (p[0] === 172 && p[1]! >= 16 && p[1]! <= 31) || (p[0] === 192 && p[1] === 168))) return invalid();
  return raw;
}

export function parseGatewayCheckReview(raw: unknown): GatewayCheckReview {
  const v = exact(raw, ["review_id", "created_at", "expires_at", "target", "source", "interface_name", "interface_index", "prefixes", "budget"]);
  if (typeof v.review_id !== "string" || !/^[A-Za-z0-9_-]{43}$/.test(v.review_id) ||
    typeof v.interface_name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(v.interface_name) ||
    !Array.isArray(v.prefixes) || v.prefixes.length < 1 || v.prefixes.length > 64 ||
    v.prefixes.some((p) => typeof p !== "string" || p.length > 128 || !/^[0-9./]+$/.test(p))) return invalid();
  const created = time(v.created_at), expires = time(v.expires_at);
  if (Date.parse(expires) - Date.parse(created) !== 30000) return invalid();
  const budget = exact(v.budget, ["max_attempts", "min_interval_ms", "attempt_timeout_ms", "total_timeout_ms", "payload_bytes", "max_icmp_request_bytes", "max_concurrent_runs", "min_run_interval_ms"]);
  const b: GatewayCheckBudget = {
    max_attempts: uint(budget.max_attempts, 3), min_interval_ms: uint(budget.min_interval_ms, 1000),
    attempt_timeout_ms: uint(budget.attempt_timeout_ms, 1000), total_timeout_ms: uint(budget.total_timeout_ms, 5000),
    payload_bytes: uint(budget.payload_bytes, 32), max_icmp_request_bytes: uint(budget.max_icmp_request_bytes, 120),
    max_concurrent_runs: uint(budget.max_concurrent_runs, 1), min_run_interval_ms: uint(budget.min_run_interval_ms, 60000),
  };
  if (b.max_attempts !== 3 || b.min_interval_ms !== 1000 || b.attempt_timeout_ms !== 1000 || b.total_timeout_ms !== 5000 ||
    b.payload_bytes !== 32 || b.max_icmp_request_bytes !== 120 || b.max_concurrent_runs !== 1 || b.min_run_interval_ms !== 60000) return invalid();
  const target = privateIPv4(v.target), source = privateIPv4(v.source);
  if (target === source) return invalid();
  const interfaceIndex = uint(v.interface_index, 2147483647);
  if (interfaceIndex === 0) return invalid();
  return { review_id: v.review_id, created_at: created, expires_at: expires, target, source,
    interface_name: v.interface_name, interface_index: interfaceIndex, prefixes: v.prefixes as string[], budget: b };
}

export function parseGatewayCheckResult(raw: unknown): GatewayCheckResult {
  const v = exact(raw, ["outcome"], ["run_id", "failure_code", "measurement"]);
  const outcome = v.outcome;
  if (outcome !== "declined" && outcome !== "completed" && outcome !== "blocked" && outcome !== "canceled" && outcome !== "failed" && outcome !== "indeterminate") return invalid();
  if (outcome === "declined") {
    if (v.run_id !== undefined || v.failure_code !== undefined || v.measurement !== undefined) return invalid();
    return { outcome };
  }
  if (typeof v.run_id !== "string" || !/^[0-9a-f]{32}$/.test(v.run_id)) return invalid();
  const result: GatewayCheckResult = { outcome, run_id: v.run_id };
  if (v.failure_code !== undefined) {
    if (typeof v.failure_code !== "string" || !["execution_failed", "canceled", "precondition_failed", "review_expired"].includes(v.failure_code)) return invalid();
    result.failure_code = v.failure_code;
  }
  if (v.measurement !== undefined) {
    const m = exact(v.measurement, ["started_at", "send_calls", "accepted_requests", "replies", "timeouts", "complete"], ["completed_at", "mean_rtt_ns"]);
    if (typeof m.complete !== "boolean") return invalid();
    result.measurement = { started_at: time(m.started_at), send_calls: uint(m.send_calls, 3), accepted_requests: uint(m.accepted_requests, 3),
      replies: uint(m.replies, 3), timeouts: uint(m.timeouts, 3), complete: m.complete };
    if (m.completed_at !== undefined) result.measurement.completed_at = time(m.completed_at);
    if (m.mean_rtt_ns !== undefined) result.measurement.mean_rtt_ns = uint(m.mean_rtt_ns, 999999999);
    const sample = result.measurement;
    const finished = sample.replies + sample.timeouts;
    if (sample.accepted_requests > sample.send_calls || sample.send_calls - sample.accepted_requests > 1 ||
      finished > sample.accepted_requests || sample.accepted_requests - finished > 1 ||
      (sample.send_calls > sample.accepted_requests && finished !== sample.accepted_requests) ||
      (sample.complete && (sample.send_calls !== 3 || sample.accepted_requests !== 3 || finished !== 3 || sample.completed_at === undefined)) ||
      (!sample.complete && sample.mean_rtt_ns !== undefined) ||
      (sample.complete && (sample.mean_rtt_ns !== undefined) !== (sample.replies > 0))) return invalid();
  }
  if ((outcome === "completed" && (result.failure_code !== undefined || !result.measurement?.complete)) ||
    (outcome === "failed" && (result.failure_code !== "execution_failed" || result.measurement?.complete)) ||
    (outcome === "canceled" && result.failure_code !== "canceled") ||
    (outcome === "indeterminate" && (result.failure_code !== "execution_failed" || result.measurement !== undefined)) ||
    (outcome === "blocked" && (!(["precondition_failed", "review_expired"].includes(result.failure_code ?? "")) || result.measurement !== undefined))) return invalid();
  return result;
}
