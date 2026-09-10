import { afterEach, describe, expect, it, vi } from "vitest";

import { createWebSetupClient, parseNetworkList, SetupRequestError } from "./setup";

const networkResponse = {
  candidates: [
    { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24", "fd00::/64"] },
  ],
  candidates_truncated: false,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("parseNetworkList", () => {
  it("reconstructs the bounded network authorization model", () => {
    const parsed = parseNetworkList({
      ...networkResponse,
      enrolled: {
        scope_id: "scope.home",
        enrolled_at: "2026-09-10T02:00:00Z",
        interface: networkResponse.candidates[0],
      },
      ignored: "not-authoritative",
    });
    expect(parsed.enrolled?.scope_id).toBe("scope.home");
    expect(parsed.candidates[0]?.interface_name).toBe("en0");
    expect(parsed).not.toHaveProperty("ignored");
  });

  it("rejects duplicate interfaces and invalid controller-domain scope ids", () => {
    expect(() => parseNetworkList({
      candidates: [networkResponse.candidates[0], networkResponse.candidates[0]],
      candidates_truncated: false,
    })).toThrow(/repeats interface/);

    expect(() => parseNetworkList({
      ...networkResponse,
      enrolled: {
        scope_id: "SCOPE HOME",
        enrolled_at: "2026-09-10T02:00:00Z",
        interface: networkResponse.candidates[0],
      },
    })).toThrow(/scope id/);
  });
});

describe("createWebSetupClient", () => {
  it("keeps one validated CSRF token in client memory and sends it on mutations", async () => {
    const calls: Array<{ path: string; init: RequestInit | undefined }> = [];
    const fetchMock = vi.fn(async (path: string, init?: RequestInit) => {
      calls.push({ path, init });
      if (path === "/api/session") {
        return jsonResponse({ csrf_token: "ccccccccccccccccccccccccccccccccccccccccccc" });
      }
      if (path === "/api/networks/enroll") {
        return jsonResponse({
          scope_id: "scope.home",
          enrolled_at: "2026-09-10T02:00:00Z",
          interface: networkResponse.candidates[0],
          changed: true,
        });
      }
      if (path === "/api/device-watch/enable") {
        return jsonResponse({
          scope_id: "scope.home",
          changed: true,
          active: true,
          state: { desired: "enabled", process: "not-applicable", verification: "unverified" },
        });
      }
      throw new Error(`unexpected path ${path}`);
    });
    vi.stubGlobal("fetch", fetchMock);

    const client = createWebSetupClient();
    await client.enrollNetwork("en0");
    await client.enableDeviceWatch();

    expect(calls.filter((call) => call.path === "/api/session")).toHaveLength(1);
    const mutations = calls.filter((call) => call.path !== "/api/session");
    expect(mutations).toHaveLength(2);
    for (const call of mutations) {
      expect(new Headers(call.init?.headers).get("X-Cozy-CSRF")).toBe("ccccccccccccccccccccccccccccccccccccccccccc");
      expect(call.init?.credentials).toBe("same-origin");
    }
    expect(JSON.parse(String(mutations[0]?.init?.body))).toEqual({ interface_name: "en0" });
  });

  it("preserves typed browser mutation errors", async () => {
    const fetchMock = vi.fn(async (path: string) => {
      if (path === "/api/session") return jsonResponse({ csrf_token: "ccccccccccccccccccccccccccccccccccccccccccc" });
      return jsonResponse({ error: "precondition_failed", message: "required prerequisites are not satisfied" }, 412);
    });
    vi.stubGlobal("fetch", fetchMock);

    const client = createWebSetupClient();
    try {
      await client.enableDeviceWatch();
      throw new Error("expected setup request to fail");
    } catch (error: unknown) {
      expect(error).toBeInstanceOf(SetupRequestError);
      expect((error as SetupRequestError).code).toBe("precondition_failed");
      expect((error as SetupRequestError).status).toBe(412);
    }
  });
});

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
