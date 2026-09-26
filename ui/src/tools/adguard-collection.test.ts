import { afterEach, describe, expect, it, vi } from "vitest";
import { createWebAdGuardCollectionClient, parseAdGuardCollectionReview, parseAdGuardCollectionResult } from "./adguard-collection";

const reviewID = "r".repeat(43);
const review = { review_id: reviewID, expires_at: "2999-01-01T00:00:00Z", endpoint: "https://192.0.2.5:3000", scope_id: "scope.home",
  interface: { interface_name: "en0", interface_index: 7, prefixes: ["192.0.2.0/24"] }, max_queries: 100, max_query_age_hours: 24 };

afterEach(() => vi.unstubAllGlobals());

describe("AdGuard Home collection browser boundary", () => {
  it("rejects malformed review and count projections", () => {
    expect(parseAdGuardCollectionReview(review)).toEqual(review);
    expect(() => parseAdGuardCollectionReview({ ...review, endpoint: "https://resolver.example" })).toThrow();
    expect(() => parseAdGuardCollectionReview({ ...review, max_queries: 10000 })).toThrow();
    expect(() => parseAdGuardCollectionReview({ ...review, interface: { ...review.interface, prefixes: [] } })).toThrow();
    expect(() => parseAdGuardCollectionResult({ outcome: "completed", result: { scope_id: "other" } }, "scope.home")).toThrow();
  });

  it("uses the current CSRF token and only the one-use review id for an approved read", async () => {
    const calls: Array<{ path: string; init: RequestInit }> = [];
    vi.stubGlobal("fetch", vi.fn(async (path: string, init: RequestInit) => {
      calls.push({ path, init });
      const payload = path === "/api/session" ? { csrf_token: "c".repeat(43) } : path.endsWith("/review") ? review :
        { outcome: "completed", result: { scope_id: "scope.home", query_log_enabled: true, read: 1, inserted: 1, deduplicated: 0,
          skipped_outside_scope: 0, skipped_without_client_ip: 0, skipped_outside_window: 0, limit_reached: false } };
      return new Response(JSON.stringify(payload), { status: 200, headers: { "Content-Type": "application/json" } });
    }));
    const client = createWebAdGuardCollectionClient();
    const pending = await client.review();
    const result = await client.decide(pending, true);
    expect(result.result?.inserted).toBe(1);
    expect(calls.map((call) => call.path)).toEqual(["/api/session", "/api/adguard/collection/review", "/api/adguard/collection/run"]);
    for (const call of calls.slice(1)) expect(new Headers(call.init.headers).get("X-Cozy-CSRF")).toBe("c".repeat(43));
    expect(JSON.parse(calls[2]!.init.body as string)).toEqual({ review_id: reviewID, approve: true });
  });
});
