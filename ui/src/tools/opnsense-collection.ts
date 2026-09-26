import { readBoundedWebJSON } from "../web-json";
import { parseAdminOrigin } from "./adguard-status";

const tokenPattern = /^[A-Za-z0-9_-]{43}$/;
const scopePattern = /^[a-z][a-z0-9._:-]{0,127}$/;
const controls = /[\u0000-\u001f\u007f]/;
const maxRowsPerFamily = 256;

export interface OPNsenseCollectionReview {
  review_id: string;
  expires_at: string;
  endpoint: string;
  scope_id: string;
  interface: { interface_name: string; interface_index: number; prefixes: string[] };
  max_rows_per_family: 256;
}

export interface OPNsenseCollectionResult {
  outcome: "completed" | "declined";
  result?: {
    scope_id: string;
    read: number;
    ipv4_total: number;
    ipv6_total: number;
    ipv4_truncated: boolean;
    ipv6_truncated: boolean;
    inserted: number;
    deduplicated: number;
    skipped_outside_scope: number;
    skipped_duplicate: number;
  };
}

function object(input: unknown): Record<string, unknown> {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("Invalid OPNsense collection response");
  return input as Record<string, unknown>;
}

export function parseOPNsenseCollectionReview(input: unknown): OPNsenseCollectionReview {
  const value = object(input);
  const iface = object(value.interface);
  if (typeof value.review_id !== "string" || !tokenPattern.test(value.review_id) ||
      typeof value.scope_id !== "string" || !scopePattern.test(value.scope_id) ||
      typeof value.expires_at !== "string" || !Number.isFinite(Date.parse(value.expires_at)) ||
      typeof iface.interface_name !== "string" || iface.interface_name.length === 0 || iface.interface_name.length > 64 || controls.test(iface.interface_name) ||
      !Number.isSafeInteger(iface.interface_index) || (iface.interface_index as number) <= 0 ||
      !Array.isArray(iface.prefixes) || iface.prefixes.length === 0 || iface.prefixes.length > 32 ||
      !iface.prefixes.every((prefix) => typeof prefix === "string" && prefix.length > 0 && prefix.length <= 128 && !controls.test(prefix)) ||
      value.max_rows_per_family !== maxRowsPerFamily) throw new Error("Invalid OPNsense collection review");
  const endpoint = parseAdminOrigin(value.endpoint);
  if (!endpoint.startsWith("https://")) throw new Error("Invalid OPNsense collection origin");
  return { review_id: value.review_id, expires_at: value.expires_at, endpoint, scope_id: value.scope_id,
    interface: { interface_name: iface.interface_name, interface_index: iface.interface_index as number, prefixes: iface.prefixes as string[] },
    max_rows_per_family: 256 };
}

export function parseOPNsenseCollectionResult(input: unknown, scopeID: string): OPNsenseCollectionResult {
  const value = object(input);
  if (value.outcome === "declined" && value.result === undefined) return { outcome: "declined" };
  if (value.outcome !== "completed") throw new Error("Invalid OPNsense collection result");
  const result = object(value.result);
  if (result.scope_id !== scopeID || typeof result.ipv4_truncated !== "boolean" || typeof result.ipv6_truncated !== "boolean") throw new Error("Invalid OPNsense collection result");
  for (const field of ["read", "inserted", "deduplicated", "skipped_outside_scope", "skipped_duplicate"] as const) {
    if (!Number.isSafeInteger(result[field]) || (result[field] as number) < 0 || (result[field] as number) > 2 * maxRowsPerFamily) throw new Error("Invalid OPNsense collection count");
  }
  for (const field of ["ipv4_total", "ipv6_total"] as const) {
    if (!Number.isSafeInteger(result[field]) || (result[field] as number) < 0 || (result[field] as number) > 1000000) throw new Error("Invalid OPNsense collection total");
  }
  if (result.ipv4_truncated !== ((result.ipv4_total as number) > maxRowsPerFamily) ||
      result.ipv6_truncated !== ((result.ipv6_total as number) > maxRowsPerFamily) ||
      result.read !== Math.min(result.ipv4_total as number, maxRowsPerFamily) + Math.min(result.ipv6_total as number, maxRowsPerFamily) ||
      result.read !== (result.inserted as number) + (result.deduplicated as number) + (result.skipped_outside_scope as number) + (result.skipped_duplicate as number)) throw new Error("Invalid OPNsense collection totals");
  return { outcome: "completed", result: result as unknown as NonNullable<OPNsenseCollectionResult["result"]> };
}

export interface OPNsenseCollectionClient {
  review(): Promise<OPNsenseCollectionReview>;
  decide(review: OPNsenseCollectionReview, approve: boolean): Promise<OPNsenseCollectionResult>;
}

export function createWebOPNsenseCollectionClient(): OPNsenseCollectionClient {
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
    if (!response.ok) throw new Error("OPNsense collection is unavailable; review current status and local history before retrying");
    return readBoundedWebJSON(response);
  }
  return {
    async review() { return parseOPNsenseCollectionReview(await mutation("/api/opnsense/collection/review", {})); },
    async decide(review, approve) {
      if (!tokenPattern.test(review.review_id) || Date.now() >= Date.parse(review.expires_at)) throw new Error("OPNsense collection review expired");
      return parseOPNsenseCollectionResult(await mutation("/api/opnsense/collection/run", { review_id: review.review_id, approve }), review.scope_id);
    },
  };
}
