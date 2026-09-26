import { beforeEach, describe, expect, it, vi } from "vitest";

import { loadDeviceActivityFromWeb } from "./activity/activity";
import { loadAppDataFromWeb } from "./app-data";
import { loadCoverageFromWeb } from "./coverage/bundle";
import { loadDevicesFromWeb } from "./devices/devices";
import { loadNetworksFromWeb } from "./setup/setup";
import { loadToolsFromWeb } from "./tools/tools";
import { loadStorageOverviewFromWeb } from "./tools/storage";

vi.mock("./activity/activity", () => ({ loadDeviceActivityFromWeb: vi.fn() }));
vi.mock("./coverage/bundle", () => ({ loadCoverageFromWeb: vi.fn() }));
vi.mock("./devices/devices", () => ({ loadDevicesFromWeb: vi.fn() }));
vi.mock("./setup/setup", () => ({ loadNetworksFromWeb: vi.fn() }));
vi.mock("./tools/tools", () => ({ loadToolsFromWeb: vi.fn() }));
vi.mock("./tools/storage", () => ({ loadStorageOverviewFromWeb: vi.fn() }));

const coverage = { as_of: "2026-09-10T12:00:00Z", reports: [] };
const devices = { configured: false, as_of: "2026-09-10T12:00:00Z", devices: [], truncated: false };
const networks = { candidates: [], candidates_truncated: false };
const tools = { status: { controller_version: "dev", started_at: "2026-09-10T11:00:00Z", config_schema_version: 1, transport: "unix" as const }, catalog_schema_version: 2, capabilities: [] };

describe("loadAppDataFromWeb", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    vi.mocked(loadCoverageFromWeb).mockResolvedValue(coverage);
    vi.mocked(loadDevicesFromWeb).mockResolvedValue(devices);
    vi.mocked(loadNetworksFromWeb).mockResolvedValue(networks);
    vi.mocked(loadToolsFromWeb).mockResolvedValue(tools);
    vi.mocked(loadStorageOverviewFromWeb).mockResolvedValue({
      as_of: "2026-09-10T12:00:00Z", quota_state: "current", database_bytes: 10,
      used_bytes: 8, reusable_bytes: 2, max_bytes: 100, filesystem_state: "unavailable",
      filesystem_supported: false, filesystem_total_bytes: 0, filesystem_available_bytes: 0,
      retention: [
        { class: "ephemeral", duration_seconds: 86400 }, { class: "short", duration_seconds: 7 * 86400 },
        { class: "standard", duration_seconds: 30 * 86400 }, { class: "audit", duration_seconds: 180 * 86400 },
      ],
    });
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

  it("keeps coverage live but marks current presence unknown when the device read fails", async () => {
    vi.mocked(loadDevicesFromWeb).mockRejectedValue(new Error("private device diagnostic"));
    const result = await loadAppDataFromWeb();
    expect(result.coverage).toBe(coverage);
    expect(result.devices).toBeNull();
    expect(result.devices_error).toBe("Device evidence is temporarily unavailable.");
    expect(result.activity).not.toBeNull();
    expect(result.networks).toBe(networks);
    expect(JSON.stringify(result)).not.toContain("private device diagnostic");
  });

  it("keeps the capability catalog available when storage is unavailable", async () => {
    vi.mocked(loadStorageOverviewFromWeb).mockRejectedValue(new Error("private database path"));
    const result = await loadAppDataFromWeb();
    expect(result.tools).toBe(tools);
    expect(result.storage).toBeNull();
    expect(result.storage_error).toBe("Storage information is temporarily unavailable.");
  });

  it("still fails the live bundle when the core coverage read fails", async () => {
    vi.mocked(loadCoverageFromWeb).mockRejectedValue(new Error("controller unavailable"));

    await expect(loadAppDataFromWeb()).rejects.toThrow("controller unavailable");
  });
});
