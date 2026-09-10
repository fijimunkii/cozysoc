import { describe, expect, it, vi } from "vitest";

import { demoCoverageRaw } from "../demo/coverage";
import { CoverageLoadError, loadCoverageFromWeb, parseCoverageBundle } from "./bundle";

describe("coverage bundle", () => {
  it("parses the bounded shared report envelope", () => {
    const bundle = parseCoverageBundle({
      as_of: "2026-09-10T00:00:00Z",
      reports: [demoCoverageRaw],
      ignored_score: 100,
    });
    expect(bundle.reports).toHaveLength(1);
    expect(bundle.reports[0]?.capability_id).toBe("device-watch");
    expect("ignored_score" in bundle).toBe(false);
  });

  it("rejects duplicate capability reports", () => {
    expect(() =>
      parseCoverageBundle({
        as_of: "2026-09-10T00:00:00Z",
        reports: [demoCoverageRaw, demoCoverageRaw],
      }),
    ).toThrow(CoverageLoadError);
  });

  it("rejects invalid timestamps and oversized collections", () => {
    expect(() => parseCoverageBundle({ as_of: "not-a-time", reports: [] })).toThrow(CoverageLoadError);
    expect(() => parseCoverageBundle({ as_of: "2026-09-10T00:00:00Z", reports: Array(33).fill(demoCoverageRaw) })).toThrow(
      CoverageLoadError,
    );
  });

  it("requires a successful same-origin JSON response", async () => {
    const fetchMock = vi.fn(async () =>
      new Response(JSON.stringify({ as_of: "2026-09-10T00:00:00Z", reports: [demoCoverageRaw] }), {
        status: 200,
        headers: { "content-type": "application/json; charset=utf-8" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const bundle = await loadCoverageFromWeb();
    expect(bundle.reports[0]?.capability_id).toBe("device-watch");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/coverage",
      expect.objectContaining({ method: "GET", credentials: "same-origin", cache: "no-store" }),
    );
    vi.unstubAllGlobals();
  });
});
