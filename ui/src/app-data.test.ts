import { beforeEach, describe, expect, it, vi } from "vitest";

import { loadDeviceActivityFromWeb } from "./activity/activity";
import { loadAppDataFromWeb } from "./app-data";
import { loadCoverageFromWeb } from "./coverage/bundle";
import { loadDevicesFromWeb } from "./devices/devices";
import { loadNetworksFromWeb } from "./setup/setup";
import { loadToolsFromWeb } from "./tools/tools";

vi.mock("./activity/activity", () => ({ loadDeviceActivityFromWeb: vi.fn() }));
vi.mock("./coverage/bundle", () => ({ loadCoverageFromWeb: vi.fn() }));
vi.mock("./devices/devices", () => ({ loadDevicesFromWeb: vi.fn() }));
vi.mock("./setup/setup", () => ({ loadNetworksFromWeb: vi.fn() }));
vi.mock("./tools/tools", () => ({ loadToolsFromWeb: vi.fn() }));

const coverage = { as_of: "2026-09-10T12:00:00Z", reports: [] };
const devices = { configured: false, as_of: "2026-09-10T12:00:00Z", devices: [], truncated: false };
const networks = { candidates: [], candidates_truncated: false };
const tools = { status: { controller_version: "dev", started_at: "2026-09-10T11:00:00Z", config_schema_version: 1, transport: "unix" as const }, catalog_schema_version: 1, capabilities: [] };

describe("loadAppDataFromWeb", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(loadCoverageFromWeb).mockResolvedValue(coverage);
    vi.mocked(loadDevicesFromWeb).mockResolvedValue(devices);
    vi.mocked(loadNetworksFromWeb).mockResolvedValue(networks);
    vi.mocked(loadToolsFromWeb).mockResolvedValue(tools);
    vi.mocked(loadDeviceActivityFromWeb).mockResolvedValue({
      configured: false,
      since: "2026-09-09T12:00:00Z",
      as_of: "2026-09-10T12:00:00Z",
      items: [],
      truncated: false,
    });
  });

  it("keeps coverage and devices usable when only activity fails", async () => {
    vi.mocked(loadDeviceActivityFromWeb).mockRejectedValue(new Error("activity projection unavailable"));

    const result = await loadAppDataFromWeb();

    expect(result.coverage).toBe(coverage);
    expect(result.devices).toBe(devices);
    expect(result.networks).toBe(networks);
    expect(result.tools).toBe(tools);
    expect(result.activity).toBeNull();
    expect(result.activity_error).toBe("activity projection unavailable");
  });

  it("keeps core evidence live when only tools fail", async () => {
    vi.mocked(loadToolsFromWeb).mockRejectedValue(new Error("capability projection unavailable"));

    const result = await loadAppDataFromWeb();

    expect(result.coverage).toBe(coverage);
    expect(result.devices).toBe(devices);
    expect(result.tools).toBeNull();
    expect(result.tools_error).toBe("capability projection unavailable");
  });

  it("still fails the live bundle when the core coverage read fails", async () => {
    vi.mocked(loadCoverageFromWeb).mockRejectedValue(new Error("controller unavailable"));

    await expect(loadAppDataFromWeb()).rejects.toThrow("controller unavailable");
  });
});
