import { afterEach, describe, expect, it, vi } from "vitest";

import { createWebSetupClient, SetupRequestError } from "../setup/setup";

const csrf = "A".repeat(43);

afterEach(() => vi.unstubAllGlobals());

describe("device label web client", () => {
  it("reuses one in-memory CSRF token and sends exact label mutations", async () => {
    const calls: Array<{ path: string; init: RequestInit | undefined }> = [];
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      calls.push({ path, init });
      if (path === "/api/session") return new Response(JSON.stringify({ csrf_token: csrf }), { status: 200, headers: { "Content-Type": "application/json" } });
      const body = JSON.parse(String(init?.body)) as { device_id: string; label: string };
      const payload: Record<string, unknown> = { device_id: body.device_id, changed: true };
      if (body.label !== "") payload.user_label = body.label;
      return new Response(JSON.stringify(payload), { status: 200, headers: { "Content-Type": "application/json" } });
    }));

    const client = createWebSetupClient();
    await expect(client.labelDevice("device.one", "Kitchen speaker")).resolves.toMatchObject({ device_id: "device.one", user_label: "Kitchen speaker" });
    await expect(client.labelDevice("device.one", "")).resolves.toMatchObject({ device_id: "device.one", user_label: "" });

    expect(calls.filter((call) => call.path === "/api/session")).toHaveLength(1);
    const mutations = calls.filter((call) => call.path === "/api/devices/label");
    expect(mutations).toHaveLength(2);
    for (const call of mutations) {
      expect(new Headers(call.init?.headers).get("X-Cozy-CSRF")).toBe(csrf);
    }
  });

  it("enforces the controller byte/trim rules before fetching CSRF", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    const client = createWebSetupClient();
    await expect(client.labelDevice("device.one", " Kitchen ")).rejects.toBeInstanceOf(SetupRequestError);
    await expect(client.labelDevice("device.one", "😀".repeat(41))).rejects.toThrow("160 UTF-8 bytes");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
