import { afterEach, describe, expect, it, vi } from "vitest";

import { demoCoverageRaw } from "../demo/coverage";
import { CoverageLoadError, loadCoverageFromWeb, parseCoverageBundle } from "./bundle";

const bootstrapToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";

function liveResponse() {
  return new Response(JSON.stringify({ as_of: "2026-09-10T00:00:00Z", reports: [demoCoverageRaw] }), {
    status: 200,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, document.title, "/");
});

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

  it("uses an existing web session for a same-origin JSON coverage read", async () => {
    const fetchMock = vi.fn(async () => liveResponse());
    vi.stubGlobal("fetch", fetchMock);
    const bundle = await loadCoverageFromWeb();
    expect(bundle.reports[0]?.capability_id).toBe("device-watch");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/coverage",
      expect.objectContaining({ method: "GET", credentials: "same-origin", cache: "no-store" }),
    );
  });

  it("exchanges the one-time fragment bootstrap before reading coverage and scrubs it from history", async () => {
    window.history.replaceState(null, document.title, `/#bootstrap=${bootstrapToken}`);
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(liveResponse());
    vi.stubGlobal("fetch", fetchMock);

    const bundle = await loadCoverageFromWeb();
    expect(bundle.reports[0]?.capability_id).toBe("device-watch");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/session");
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(
      expect.objectContaining({
        method: "POST",
        credentials: "same-origin",
        cache: "no-store",
        body: JSON.stringify({ bootstrap: bootstrapToken }),
      }),
    );
    expect(fetchMock.mock.calls[1]?.[0]).toBe("/api/coverage");
    expect(window.location.hash).toBe("");
  });

  it("fails closed on a malformed bootstrap and still scrubs it from history", async () => {
    window.history.replaceState(null, document.title, "/#bootstrap=short");
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    await expect(loadCoverageFromWeb()).rejects.toThrow(CoverageLoadError);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(window.location.hash).toBe("");
  });

  it("gives an actionable error when the browser session is missing", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 401 })));
    await expect(loadCoverageFromWeb()).rejects.toThrow(/authenticated local URL/);
  });
});
