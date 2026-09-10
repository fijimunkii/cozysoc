import { afterEach, describe, expect, it, vi } from "vitest";
import { loadLocalQualityFromWeb, parseLocalQuality } from "./local-quality";
import { qualityAsOf, qualityRaw } from "./fixtures.test-helper";
import { qualityExpired, stampQuality } from "./freshness";

afterEach(() => { vi.unstubAllGlobals(); });

describe("local interface browser contract", () => {
  it("keeps unenrolled, up, down and unmeasured distinct", () => {
    expect(parseLocalQuality({ enrolled: false, as_of: qualityAsOf })).toEqual({ enrolled: false, as_of: qualityAsOf });
    expect(parseLocalQuality(qualityRaw()).enrolled).toBe(true);
    const down = qualityRaw(); down.check.administrative_up = false; down.check.state = "issue-observed";
    const parsed = parseLocalQuality(down);
    expect(parsed.enrolled && parsed.check.administrative_up).toBe(false);
    for (const gap of ["network-changed", "permission-required", "source-unavailable", "unsupported"]) {
      const raw = qualityRaw(); raw.check.state = "not-measured"; raw.check.confidence = "unknown"; raw.check.gap = gap; delete raw.check.administrative_up;
      const result = parseLocalQuality(raw);
      expect(result.enrolled && result.check.gap).toBe(gap);
      expect(result.enrolled && result.check.administrative_up).toBeUndefined();
    }
  });

  const invalidEdits: Record<string, (raw: ReturnType<typeof qualityRaw>) => void> = {
    "unenrolled evidence": (r) => { r.enrolled = false; },
    "native field": (r) => { r.limitations = ["private"]; },
    "native observer id": (r) => { r.observer.scope_id = "private"; },
    "markup interface": (r) => { r.observer.interface_name = "<script>"; },
    "long interface": (r) => { r.observer.interface_name = "a".repeat(65); },
    "zero index": (r) => { r.observer.interface_index = 0; },
    "fractional index": (r) => { r.observer.interface_index = 1.5; },
    "unbounded index": (r) => { r.observer.interface_index = 2147483648; },
    "native guidance": (r) => { r.check.summary = "private"; },
    "extra metrics": (r) => { r.check.latency = 0; },
    "unsupported source": (r) => { r.check.source = "ping"; },
    "unsupported layer": (r) => { r.check.layer = "internet"; },
    "unsupported method": (r) => { r.check.method = "https-request"; },
    "global verdict": (r) => { r.check.state = "healthy"; },
    "inflated confidence": (r) => { r.check.confidence = "high"; },
    "contradiction": (r) => { r.check.administrative_up = false; },
    "missing metric": (r) => { delete r.check.administrative_up; },
    "null metric": (r) => { r.check.administrative_up = null; },
    "gap with metric": (r) => { r.check.state = "not-measured"; r.check.confidence = "unknown"; r.check.gap = "unsupported"; },
    "measured gap": (r) => { r.check.gap = "network-changed"; },
    "invalid timestamp": (r) => { r.as_of = "2026-02-30T12:00:00Z"; },
    "missing timezone": (r) => { r.as_of = "2026-09-10T12:00:00"; },
    "future start": (r) => { r.check.started_at = "2026-09-10T12:00:01.000Z"; },
    "overlong read": (r) => { r.check.started_at = "2026-09-10T11:59:29.000Z"; },
    "completion mismatch": (r) => { r.check.completed_at = "2026-09-10T11:59:59.000Z"; },
    "inflated freshness": (r) => { r.check.fresh_until = "2026-09-10T12:01:00.000Z"; },
  };
  for (const [name, edit] of Object.entries(invalidEdits)) {
    it(`rejects ${name}`, () => { const raw = qualityRaw(); edit(raw); expect(() => parseLocalQuality(raw)).toThrow("sample is invalid"); });
  }
  it("rejects malformed containers and invented gap reasons", () => {
    for (const raw of [null, [], {}, "hello", { enrolled: "false", as_of: qualityAsOf }]) expect(() => parseLocalQuality(raw)).toThrow();
    const raw = qualityRaw(); raw.check.state = "not-measured"; raw.check.confidence = "unknown"; delete raw.check.administrative_up; raw.check.gap = "controller-sleep";
    expect(() => parseLocalQuality(raw)).toThrow();
  });

  it("loads only the fixed authenticated read with caller cancellation and no caching or redirects", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify(qualityRaw()), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    const controller = new AbortController();
    expect((await loadLocalQualityFromWeb(controller.signal)).enrolled).toBe(true);
    expect(fetcher).toHaveBeenCalledWith("/api/network-quality", { method: "GET", credentials: "same-origin", cache: "no-store", redirect: "error", signal: controller.signal });
  });
  it("rejects unavailable, oversized, non-JSON and malformed responses", async () => {
    for (const response of [
      new Response("private diagnostics", { status: 503 }),
      new Response("private session", { status: 401 }),
      new Response("{}", { headers: { "Content-Type": "text/html" } }),
      new Response(" ".repeat(8193), { headers: { "Content-Type": "application/json" } }),
      new Response("{", { headers: { "Content-Type": "application/json" } }),
    ]) {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response));
      await expect(loadLocalQualityFromWeb(new AbortController().signal)).rejects.toThrow();
    }
  });
});

describe("sample freshness", () => {
  const wall = Date.parse(qualityAsOf);
  it("expires at the exact evidence deadline and cannot refresh a replay", () => {
    const data = parseLocalQuality(qualityRaw());
    const receipt = stampQuality(data, { wall, monotonic: 100 }, { wall, monotonic: 100 });
    expect(qualityExpired(receipt, { wall: wall + 29999, monotonic: 30099 })).toBe(false);
    expect(qualityExpired(receipt, { wall: wall + 30000, monotonic: 30100 })).toBe(true);
    const replay = stampQuality(data, { wall: wall + 60000, monotonic: 100 }, { wall: wall + 60000, monotonic: 100 });
    expect(qualityExpired(replay, replay.received)).toBe(true);
  });
  it("subtracts request delay, fails closed on clock rollback, and uses monotonic elapsed time", () => {
    const data = parseLocalQuality(qualityRaw());
    const receipt = stampQuality(data, { wall, monotonic: 100 }, { wall: wall + 5000, monotonic: 5100 });
    expect(receipt.expiresMonotonic).toBe(30100);
    expect(qualityExpired(receipt, { wall: wall + 4999, monotonic: 5200 })).toBe(true);
    expect(qualityExpired(receipt, { wall: wall + 5000, monotonic: 30100 })).toBe(true);
    expect(qualityExpired(receipt, { wall: wall + 5000, monotonic: 5099 })).toBe(true);
    const invalid = stampQuality(data, { wall: wall + 5000, monotonic: 100 }, { wall, monotonic: 101 });
    expect(qualityExpired(invalid, invalid.received)).toBe(true);
    const future = stampQuality(data, { wall: wall - 5000, monotonic: 100 }, { wall: wall - 4000, monotonic: 1100 });
    expect(qualityExpired(future, future.received)).toBe(true);
  });
});
