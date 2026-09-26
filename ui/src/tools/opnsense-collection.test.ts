import { afterEach, describe, expect, it, vi } from "vitest";
import { createWebOPNsenseCollectionClient, parseOPNsenseCollectionReview, parseOPNsenseCollectionResult } from "./opnsense-collection";

const reviewID = "r".repeat(43);
const review = { review_id: reviewID, expires_at: "2999-01-01T00:00:00Z", endpoint: "https://192.168.50.1", scope_id: "scope.home",
  interface: { interface_name: "en0", interface_index: 7, prefixes: ["192.168.50.0/24"] }, max_rows_per_family: 256 };

afterEach(() => vi.unstubAllGlobals());

describe("OPNsense collection browser boundary", () => {
  it("rejects unsafe review and inconsistent result projections", () => {
    expect(parseOPNsenseCollectionReview(review)).toEqual(review);
    expect(() => parseOPNsenseCollectionReview({ ...review, endpoint: "http://192.168.50.1" })).toThrow();
    expect(() => parseOPNsenseCollectionReview({ ...review, endpoint: "https://key:secret@192.168.50.1" })).toThrow();
    expect(() => parseOPNsenseCollectionReview({ ...review, max_rows_per_family: 512 })).toThrow();
    expect(() => parseOPNsenseCollectionReview({ ...review, interface: { ...review.interface, prefixes: [] } })).toThrow();
    expect(() => parseOPNsenseCollectionResult({ outcome: "completed", result: { scope_id: "other" } }, "scope.home")).toThrow();
    expect(() => parseOPNsenseCollectionResult({ outcome: "completed", result: { scope_id: "scope.home", read: 2, ipv4_total: 2, ipv6_total: 0,
      ipv4_truncated: true, ipv6_truncated: false, inserted: 2, deduplicated: 0, skipped_outside_scope: 0, skipped_duplicate: 0 } }, "scope.home")).toThrow();
    expect(parseOPNsenseCollectionResult({ outcome: "completed", result: { scope_id: "scope.home", read: 256, ipv4_total: 257, ipv6_total: 0,
      ipv4_truncated: true, ipv6_truncated: false, inserted: 256, deduplicated: 0, skipped_outside_scope: 0, skipped_duplicate: 0 } }, "scope.home").result?.ipv4_truncated).toBe(true);
  });

  it("sends only the one-use review ID with the current CSRF token", async () => {
    const calls: Array<{ path: string; init: RequestInit }> = [];
    vi.stubGlobal("fetch", vi.fn(async (path: string, init: RequestInit) => {
      calls.push({ path, init });
      const payload = path === "/api/session" ? { csrf_token: "c".repeat(43) } : path.endsWith("/review") ? review :
        { outcome: "completed", result: { scope_id: "scope.home", read: 2, ipv4_total: 2, ipv6_total: 0,
          ipv4_truncated: false, ipv6_truncated: false, inserted: 1, deduplicated: 0, skipped_outside_scope: 1, skipped_duplicate: 0 } };
      return new Response(JSON.stringify(payload), { status: 200, headers: { "Content-Type": "application/json" } });
    }));
    const client = createWebOPNsenseCollectionClient();
    const pending = await client.review();
    const result = await client.decide(pending, true);
    expect(result.result?.inserted).toBe(1);
    expect(calls.map((call) => call.path)).toEqual(["/api/session", "/api/opnsense/collection/review", "/api/opnsense/collection/run"]);
    for (const call of calls.slice(1)) expect(new Headers(call.init.headers).get("X-Cozy-CSRF")).toBe("c".repeat(43));
    expect(JSON.parse(calls[2]!.init.body as string)).toEqual({ review_id: reviewID, approve: true });
  });
});
