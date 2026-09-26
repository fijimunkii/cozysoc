import { readBoundedWebJSON } from "../web-json";
import { parseAdminOrigin } from "./adguard-status";

const tokenPattern = /^[A-Za-z0-9_-]{43}$/;
const scopePattern = /^[a-z][a-z0-9._:-]{0,127}$/;
const controls = /[\u0000-\u001f\u007f]/;

export interface AdGuardCollectionReview {
  review_id: string;
  expires_at: string;
  endpoint: string;
  scope_id: string;
  interface: { interface_name: string; interface_index: number; prefixes: string[] };
  max_queries: 100;
  max_query_age_hours: 24;
}

export interface AdGuardCollectionResult {
  outcome: "completed" | "declined";
  result?: {
    scope_id: string;
    query_log_enabled: boolean;
    read: number;
    inserted: number;
    deduplicated: number;
    skipped_outside_scope: number;
    skipped_without_client_ip: number;
    skipped_outside_window: number;
    limit_reached: boolean;
  };
}

function object(input: unknown): Record<string, unknown> {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("Invalid AdGuard Home collection response");
  return input as Record<string, unknown>;
}

export function parseAdGuardCollectionReview(input: unknown): AdGuardCollectionReview {
  const value = object(input);
  const iface = object(value.interface);
  if (typeof value.review_id !== "string" || !tokenPattern.test(value.review_id) ||
      typeof value.scope_id !== "string" || !scopePattern.test(value.scope_id) ||
      typeof value.expires_at !== "string" || !Number.isFinite(Date.parse(value.expires_at)) ||
      typeof iface.interface_name !== "string" || iface.interface_name.length === 0 || iface.interface_name.length > 64 || controls.test(iface.interface_name) ||
      !Number.isSafeInteger(iface.interface_index) || (iface.interface_index as number) <= 0 ||
      !Array.isArray(iface.prefixes) || iface.prefixes.length === 0 || iface.prefixes.length > 32 ||
      !iface.prefixes.every((prefix) => typeof prefix === "string" && prefix.length > 0 && prefix.length <= 128 && !controls.test(prefix)) ||
      value.max_queries !== 100 || value.max_query_age_hours !== 24) throw new Error("Invalid AdGuard Home collection review");
  return { review_id: value.review_id, expires_at: value.expires_at, endpoint: parseAdminOrigin(value.endpoint), scope_id: value.scope_id,
    interface: { interface_name: iface.interface_name, interface_index: iface.interface_index as number, prefixes: iface.prefixes as string[] },
    max_queries: 100, max_query_age_hours: 24 };
}

export function parseAdGuardCollectionResult(input: unknown, scopeID: string): AdGuardCollectionResult {
  const value = object(input);
  if (value.outcome === "declined" && value.result === undefined) return { outcome: "declined" };
  if (value.outcome !== "completed") throw new Error("Invalid AdGuard Home collection result");
  const result = object(value.result);
  if (result.scope_id !== scopeID || typeof result.query_log_enabled !== "boolean" || typeof result.limit_reached !== "boolean") throw new Error("Invalid AdGuard Home collection result");
  for (const field of ["read", "inserted", "deduplicated", "skipped_outside_scope", "skipped_without_client_ip", "skipped_outside_window"] as const) {
    if (!Number.isSafeInteger(result[field]) || (result[field] as number) < 0 || (result[field] as number) > 100) throw new Error("Invalid AdGuard Home collection count");
  }
  if ((result.read as number) > 100 || result.limit_reached !== (result.read === 100)) throw new Error("Invalid AdGuard Home collection limit");
  return { outcome: "completed", result: result as unknown as NonNullable<AdGuardCollectionResult["result"]> };
}

export interface AdGuardCollectionClient {
  review(): Promise<AdGuardCollectionReview>;
  decide(review: AdGuardCollectionReview, approve: boolean): Promise<AdGuardCollectionResult>;
}

export function createWebAdGuardCollectionClient(): AdGuardCollectionClient {
  let csrf: string | undefined;
  async function mutation(path: string, body: unknown): Promise<unknown> {
    if (!csrf) {
      const response = await fetch("/api/session", { method: "GET", credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
      if (!response.ok) throw new Error("The local web session is unavailable");
      const session = object(await readBoundedWebJSON(response));
      if (typeof session.csrf_token !== "string" || !tokenPattern.test(session.csrf_token)) throw new Error("Invalid local web session");
      csrf = session.csrf_token;
    }
    const response = await fetch(path, { method: "POST", credentials: "same-origin", cache: "no-store",
      headers: { Accept: "application/json", "Content-Type": "application/json", "X-Cozy-CSRF": csrf }, body: JSON.stringify(body) });
    if (!response.ok) throw new Error("AdGuard Home collection is unavailable; review current status and local history before retrying");
    return readBoundedWebJSON(response);
  }
  return {
    async review() { return parseAdGuardCollectionReview(await mutation("/api/adguard/collection/review", {})); },
    async decide(review, approve) {
      if (!tokenPattern.test(review.review_id) || Date.now() >= Date.parse(review.expires_at)) throw new Error("AdGuard Home collection review expired");
      return parseAdGuardCollectionResult(await mutation("/api/adguard/collection/run", { review_id: review.review_id, approve }), review.scope_id);
    },
  };
}
