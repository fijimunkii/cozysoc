import { afterEach, describe, expect, it, vi } from "vitest";

import { createWebSetupClient, loadDeviceSplitsFromWeb, parseDeviceSplitList } from "../setup/setup";

afterEach(() => vi.unstubAllGlobals());

describe("device split web contract", () => {
  it("rejects malformed, duplicate, and cross-scope mappings", () => {
    const split = { observation_id: "obs.one", source_device_id: "device.a", target_device_id: "device.b", created_at: "2026-09-10T12:00:00Z" };
    expect(() => parseDeviceSplitList({ configured: true, scope_id: "scope.home", splits: [split, split] })).toThrow();
    expect(() => parseDeviceSplitList({ configured: false, scope_id: "scope.home", splits: [] })).toThrow();
    expect(() => parseDeviceSplitList({ configured: true, scope_id: "scope.home", splits: [{ ...split, target_device_id: "device.a" }] })).toThrow();
  });

  it("loads splits and sends reviewed changes with CSRF", async () => {
    const calls: Array<{ path: string; init: RequestInit | undefined }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push({ path, init });
      if (path === "/api/session") return new Response(JSON.stringify({ csrf_token: "A".repeat(43) }), { status: 200, headers: { "Content-Type": "application/json" } });
      if (path === "/api/devices/splits") return new Response(JSON.stringify({ configured: true, scope_id: "scope.home", splits: [] }), { status: 200, headers: { "Content-Type": "application/json" } });
      const body = JSON.parse(String(init?.body)) as { source_device_id?: string; observation_id: string; target_device_id?: string };
      return new Response(JSON.stringify({ ...body, target_device_id: path.endsWith("/split") ? body.target_device_id ?? "device.created" : undefined, changed: true }), { status: 200, headers: { "Content-Type": "application/json" } });
    }));
    await expect(loadDeviceSplitsFromWeb()).resolves.toMatchObject({ scope_id: "scope.home", splits: [] });
    const client = createWebSetupClient();
    await expect(client.splitObservation("device.a", "obs.one")).resolves.toMatchObject({ target_device_id: "device.created", changed: true });
    await expect(client.unsplitObservation("obs.one")).resolves.toMatchObject({ changed: true });
    expect(calls.filter((call) => call.path === "/api/session")).toHaveLength(1);
    for (const call of calls.filter((item) => item.path === "/api/devices/split" || item.path === "/api/devices/unsplit")) {
      expect(new Headers(call.init?.headers).get("X-Cozy-CSRF")).toBe("A".repeat(43));
    }
  });

  it("does not send invalid identities", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const client = createWebSetupClient();
    await expect(client.splitObservation("../outside", "obs.one")).rejects.toThrow();
    await expect(client.splitObservation("device.a", "obs.one", "device.a")).rejects.toThrow();
    await expect(client.unsplitObservation("../outside")).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
