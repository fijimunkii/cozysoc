import { afterEach, describe, expect, it, vi } from "vitest";

import { createWebSetupClient, loadDeviceMergesFromWeb, parseDeviceMergeList } from "../setup/setup";

afterEach(() => vi.unstubAllGlobals());

const csrf = "A".repeat(43);

describe("device correction web contract", () => {
  it("rejects malformed or chained mappings", () => {
    expect(() => parseDeviceMergeList({ configured: true, scope_id: "scope.home", merges: [{ source_device_id: "device.a", target_device_id: "device.b", created_at: "2026-09-10T12:00:00Z" }, { source_device_id: "device.b", target_device_id: "device.c", created_at: "2026-09-10T12:00:00Z" }] })).toThrow();
    expect(() => parseDeviceMergeList({ configured: false, scope_id: "scope.home", merges: [] })).toThrow();
  });

  it("loads bounded active mappings and sends exact reviewed changes with CSRF", async () => {
    const calls: Array<{ path: string; init: RequestInit | undefined }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push({ path, init });
      if (path === "/api/session") return new Response(JSON.stringify({ csrf_token: csrf }), { status: 200, headers: { "Content-Type": "application/json" } });
      if (path === "/api/devices/merges") return new Response(JSON.stringify({ configured: true, scope_id: "scope.home", merges: [{ source_device_id: "device.a", target_device_id: "device.b", created_at: "2026-09-10T12:00:00Z" }] }), { status: 200, headers: { "Content-Type": "application/json" } });
      const body = JSON.parse(String(init?.body)) as { source_device_id: string; target_device_id?: string };
      return new Response(JSON.stringify({ ...body, changed: true }), { status: 200, headers: { "Content-Type": "application/json" } });
    }));
    await expect(loadDeviceMergesFromWeb()).resolves.toMatchObject({ scope_id: "scope.home", merges: [{ source_device_id: "device.a" }] });
    const client = createWebSetupClient();
    await expect(client.mergeDevices("device.a", "device.b")).resolves.toMatchObject({ changed: true });
    await expect(client.unmergeDevice("device.a")).resolves.toMatchObject({ changed: true });
    expect(calls.filter((call) => call.path === "/api/session")).toHaveLength(1);
    const mutations = calls.filter((call) => call.path === "/api/devices/merge" || call.path === "/api/devices/unmerge");
    expect(mutations).toHaveLength(2);
    for (const call of mutations) expect(new Headers(call.init?.headers).get("X-Cozy-CSRF")).toBe(csrf);
  });

  it("does not send invalid IDs", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const client = createWebSetupClient();
    await expect(client.mergeDevices("device.a", "device.a")).rejects.toThrow();
    await expect(client.unmergeDevice("../outside")).rejects.toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
