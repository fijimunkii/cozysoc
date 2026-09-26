import { afterEach, describe, expect, it, vi } from "vitest";
import { adminLinkSharesWebCookieHost, loadAdGuardStatus, parseAdGuardStatus } from "./adguard-status";

const connected = { connected: true, endpoint: "https://192.0.2.5:3000", version: "v0.107.79", running: true, protection_enabled: true, filtering_enabled: true, query_log_enabled: true, anonymized_clients: false };

afterEach(() => vi.unstubAllGlobals());

describe("AdGuard Home browser status", () => {
  it("accepts only literal-IP admin origins and omits unknown private fields", () => {
    expect(parseAdGuardStatus({ ...connected, username: "private", password: "secret" })).toEqual(connected);
    expect(parseAdGuardStatus({ ...connected, endpoint: "http://127.0.0.1:3000" }).endpoint).toBe("http://127.0.0.1:3000");
    expect(parseAdGuardStatus({ ...connected, endpoint: "https://[2001:db8::5]:3000" }).endpoint).toBe("https://[2001:db8::5]:3000");
    for (const endpoint of ["http://192.0.2.5", "https://example.com", "https://user:pass@192.0.2.5", "javascript:alert(1)", "https://192.0.2.5/path", "https://192.0.2.5#frag"]) {
      expect(() => parseAdGuardStatus({ ...connected, endpoint })).toThrow();
    }
    expect(() => parseAdGuardStatus({ ...connected, running: "yes" })).toThrow();
  });

  it("loads only the fixed authenticated local route", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(connected), { status: 200, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(loadAdGuardStatus(new AbortController().signal)).resolves.toEqual(connected);
    expect(fetchMock.mock.calls[0]?.[0]).toBe("/api/adguard/status");
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "GET", credentials: "same-origin", cache: "no-store" });
  });

  it("withholds direct links to services on the web session cookie host, regardless of port", () => {
    expect(adminLinkSharesWebCookieHost("http://127.0.0.1:3000", "127.0.0.1")).toBe(true);
    expect(adminLinkSharesWebCookieHost("https://[::1]:3000", "[::1]")).toBe(true);
    expect(adminLinkSharesWebCookieHost("https://192.0.2.5:3000", "127.0.0.1")).toBe(false);
  });
});
