export interface ResolverSettings {
  endpoint: string;
  name: string;
  family: "ipv4" | "ipv6";
  transport: "udp";
  query_type: "A" | "AAAA";
  expect: "answer" | "nxdomain" | "no-data";
  destination_scope: "enrolled-prefix" | "exact-endpoint";
}
export interface ResolverSelection { selection_id: string; settings: ResolverSettings }
export interface ResolverReview extends ResolverSelection {
  review_id: string; created_at: string; expires_at: string; source: string;
  interface_name: string; interface_index: number; prefixes: string[];
  outside_enrolled_prefixes: boolean; may_forward_upstream: boolean;
  budget: { max_send_calls: number; max_request_bytes: number; max_reply_bytes: number;
    max_received_datagrams: number; max_receive_calls: number; exchange_timeout_ms: number;
    total_timeout_ms: number; max_concurrent_runs: number; min_run_interval_ms: number };
}
export interface ResolverCheckResult { outcome: "declined" | "completed" | "failed" | "canceled" | "indeterminate" | "blocked"; run_id?: string; failure_code?: string }
export interface ResolverCheckClient {
  reviewResolver(selectionID: string): Promise<ResolverReview>;
  decideResolver(reviewID: string, approve: boolean): Promise<ResolverCheckResult>;
}
const selectionIDPattern = /^selection\.[0-9a-f]{32}$/;
function invalid(): never { throw new Error("Resolver check response is invalid."); }
function exact(raw: unknown, required: string[], optional: string[] = []): Record<string, unknown> {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return invalid();
  const v = raw as Record<string, unknown>;
  if (required.some((k) => !Object.hasOwn(v, k)) || Object.keys(v).some((k) => !required.includes(k) && !optional.includes(k))) return invalid();
  return v;
}
function number(raw: unknown, low: number, high: number): number {
  if (typeof raw !== "number" || !Number.isSafeInteger(raw) || raw < low || raw > high) return invalid();
  return raw;
}
function time(raw: unknown): string {
  if (typeof raw !== "string" || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(raw) || !Number.isFinite(Date.parse(raw))) return invalid();
  return raw;
}
function settings(raw: unknown): ResolverSettings {
  const v = exact(raw, ["endpoint", "name", "family", "transport", "query_type", "expect", "destination_scope"]);
  if (typeof v.endpoint !== "string" || v.endpoint.length > 64 || !/^(?:\[[0-9a-f:]+\]|[0-9.]+):53$/.test(v.endpoint) ||
    typeof v.name !== "string" || v.name.length > 254 || !/^[A-Za-z0-9._-]+\.$/.test(v.name) ||
    (v.family !== "ipv4" && v.family !== "ipv6") || v.transport !== "udp" || (v.query_type !== "A" && v.query_type !== "AAAA") ||
    !["answer", "nxdomain", "no-data"].includes(String(v.expect)) || !["enrolled-prefix", "exact-endpoint"].includes(String(v.destination_scope))) return invalid();
  return v as unknown as ResolverSettings;
}
function selection(raw: unknown): ResolverSelection {
  const v = exact(raw, ["selection_id", "settings"]);
  if (typeof v.selection_id !== "string" || !selectionIDPattern.test(v.selection_id)) return invalid();
  return { selection_id: v.selection_id, settings: settings(v.settings) };
}
export function parseResolverSelections(raw: unknown): ResolverSelection[] {
  const v = exact(raw, ["items"]);
  if (!Array.isArray(v.items) || v.items.length > 16) return invalid();
  const items = v.items.map(selection);
  if (new Set(items.map((i) => i.selection_id)).size !== items.length) return invalid();
  return items;
}
export function parseResolverReview(raw: unknown): ResolverReview {
  const v = exact(raw, ["review_id", "selection_id", "settings", "created_at", "expires_at", "source", "interface_name", "interface_index", "prefixes", "outside_enrolled_prefixes", "may_forward_upstream", "budget"]);
  if (typeof v.review_id !== "string" || !/^[A-Za-z0-9_-]{43}$/.test(v.review_id) || typeof v.selection_id !== "string" || !selectionIDPattern.test(v.selection_id) ||
    typeof v.source !== "string" || v.source.length > 64 || !/^[0-9a-f:.]+$/.test(v.source) || typeof v.interface_name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/.test(v.interface_name) ||
    !Array.isArray(v.prefixes) || v.prefixes.length < 1 || v.prefixes.length > 64 || v.prefixes.some((p) => typeof p !== "string" || p.length > 128 || !/^[0-9a-f:./]+$/.test(p)) ||
    typeof v.outside_enrolled_prefixes !== "boolean" || v.may_forward_upstream !== true) return invalid();
  const created = time(v.created_at), expires = time(v.expires_at);
  if (Date.parse(expires) - Date.parse(created) !== 30000) return invalid();
  const b = exact(v.budget, ["max_send_calls", "max_request_bytes", "max_reply_bytes", "max_received_datagrams", "max_receive_calls", "exchange_timeout_ms", "total_timeout_ms", "max_concurrent_runs", "min_run_interval_ms"]);
  const budget = { max_send_calls:number(b.max_send_calls,1,1), max_request_bytes:number(b.max_request_bytes,17,512), max_reply_bytes:number(b.max_reply_bytes,512,512),
    max_received_datagrams:number(b.max_received_datagrams,16,16), max_receive_calls:number(b.max_receive_calls,512,512), exchange_timeout_ms:number(b.exchange_timeout_ms,2000,2000),
    total_timeout_ms:number(b.total_timeout_ms,5000,5000), max_concurrent_runs:number(b.max_concurrent_runs,1,1), min_run_interval_ms:number(b.min_run_interval_ms,60000,60000) };
  return { review_id:v.review_id, selection_id:v.selection_id, settings:settings(v.settings), created_at:created, expires_at:expires,
    source:v.source, interface_name:v.interface_name, interface_index:number(v.interface_index,1,2147483647), prefixes:v.prefixes as string[],
    outside_enrolled_prefixes:v.outside_enrolled_prefixes, may_forward_upstream:true, budget };
}
export function parseResolverResult(raw: unknown): ResolverCheckResult {
  const v = exact(raw, ["outcome"], ["run_id", "failure_code"]);
  if (v.outcome === "declined") {
    if (v.run_id !== undefined || v.failure_code !== undefined) return invalid();
    return { outcome:"declined" };
  }
  if (!["completed", "failed", "canceled", "indeterminate", "blocked"].includes(String(v.outcome)) || typeof v.run_id !== "string" || !/^[0-9a-f]{32}$/.test(v.run_id)) return invalid();
  const expected: Record<string, string[]> = { completed:[""], failed:["execution_failed"], canceled:["canceled"], indeterminate:["execution_failed"], blocked:["precondition_failed", "review_expired"] };
  const failure = v.failure_code === undefined ? "" : v.failure_code;
  if (typeof failure !== "string" || !expected[String(v.outcome)]?.includes(failure)) return invalid();
  return { outcome:v.outcome as ResolverCheckResult["outcome"], run_id:v.run_id, ...(failure ? { failure_code:failure } : {}) };
}
