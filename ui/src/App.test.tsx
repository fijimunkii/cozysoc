import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { AppData } from "./app-data";
import { App } from "./App";
import { parseDeviceActivity } from "./activity/activity";
import { parseCoverageBundle } from "./coverage/bundle";
import { parseDeviceList } from "./devices/devices";
import { demoCoverageRaw } from "./demo/coverage";
import { demoCapabilitiesRaw, demoStatusRaw } from "./demo/tools";
import { parseNetworkList } from "./setup/setup";
import { parseToolsSnapshot } from "./tools/tools";

const liveData: AppData = {
  coverage: parseCoverageBundle({ as_of: "2026-09-10T01:00:00Z", reports: [demoCoverageRaw] }),
  activity: parseDeviceActivity({ configured: true, scope_id: "scope.home", since: "2026-09-09T01:00:00Z", as_of: "2026-09-10T01:00:00Z", items: [{ id: "obs.one", kind: "observed", at: "2026-09-10T00:59:00Z", device_id: "device.one", user_label: "Living Room TV", address_family: "ipv4", address: "192.168.1.20", hardware_address: "02:00:00:00:00:01", source: { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T00:59:01Z", attribution: "device-watch:arp-cache" } }], truncated: false }),
  tools: parseToolsSnapshot(demoStatusRaw, demoCapabilitiesRaw),
  devices: parseDeviceList({
    configured: true,
    scope_id: "scope.home",
    as_of: "2026-09-10T01:00:00Z",
    devices: [{ id: "device.one", user_label: "Living Room TV", first_seen: "2026-09-09T20:00:00Z", last_seen: "2026-09-10T00:59:00Z", state: "visible" }],
    truncated: false,
  }),
  networks: parseNetworkList({
    candidates: [{ interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] }],
    candidates_truncated: false,
    enrolled: {
      scope_id: "scope.home",
      enrolled_at: "2026-09-09T19:00:00Z",
      interface: { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] },
    },
  }),
};

describe("App product navigation", () => {
  it("keeps coverage visible while a failed device projection makes presence and setup unavailable", async () => {
    const partial: AppData = { ...liveData, devices: null, devices_error: "Device evidence is temporarily unavailable." };
    const loadData = vi.fn().mockResolvedValueOnce(partial).mockResolvedValue(liveData);
    render(<App loadData={loadData} />);
    await screen.findByRole("status", { name: "Live controller data" });
    expect(screen.getByText("Unavailable", { selector: "strong" })).toBeTruthy();
    expect(screen.getByText(/Current presence is unknown; coverage is shown separately/)).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Device status is unavailable" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Pause Device Watch" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByRole("heading", { name: "Device evidence is temporarily unavailable" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry device evidence" }));
    await waitFor(() => expect(loadData).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("Living Room TV")).toBeTruthy();
  });

  it("opens guided setup directly from unconfigured Devices", async () => {
    const unconfigured: AppData = {
      ...liveData,
      coverage: parseCoverageBundle({ as_of: "2026-09-10T01:00:00Z", reports: [] }),
      devices: parseDeviceList({ configured: false, as_of: "2026-09-10T01:00:00Z", devices: [], truncated: false }),
      networks: parseNetworkList({ candidates: [], candidates_truncated: false }),
    };
    render(<App loadData={async () => unconfigured} />);
    await screen.findByRole("status", { name: "Live controller data" });
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByRole("heading", { name: "Device visibility is not configured" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Open guided setup" }));
    expect(screen.getByRole("heading", { name: "Your home at a glance" })).toBe(document.activeElement);
  });

  it("offers coverage and a fresh read when a configured network has no devices yet", async () => {
    const empty: AppData = { ...liveData, devices: parseDeviceList({ configured: true, scope_id: "scope.home", as_of: "2026-09-10T01:00:00Z", devices: [], truncated: false }) };
    const loadData = vi.fn(async () => empty);
    render(<App loadData={loadData} />);
    await screen.findByRole("status", { name: "Live controller data" });
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByText("No devices are visible yet")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Refresh device evidence" }));
    await waitFor(() => expect(loadData).toHaveBeenCalledTimes(2));
    fireEvent.click(screen.getByRole("button", { name: "Review coverage" }));
    expect(screen.getByRole("heading", { name: "Know what is visible. Know what is not." })).toBe(document.activeElement);
  });

  it("moves keyboard focus to the selected section without stealing it on a live refresh", async () => {
    const updated = { ...liveData, devices: parseDeviceList({
      configured: true,
      scope_id: "scope.home",
      as_of: "2026-09-10T01:01:00Z",
      devices: [...liveData.devices!.devices, { id: "device.two", first_seen: "2026-09-10T01:01:00Z", last_seen: "2026-09-10T01:01:00Z", state: "visible" }],
      truncated: false,
    }) };
    const loadData = vi.fn().mockResolvedValueOnce(liveData).mockResolvedValue(updated);
    render(<App loadData={loadData} />);
    await screen.findByRole("status", { name: "Live controller data" });
    const heading = screen.getByRole("heading", { level: 1 });
    expect(document.activeElement).not.toBe(heading);

    const devices = screen.getByRole("button", { name: "Devices" });
    devices.focus();
    fireEvent.click(devices);
    expect(heading.textContent).toBe("What Cozy SOC has actually seen");
    expect(document.activeElement).toBe(heading);

    fireEvent.click(screen.getByRole("button", { name: "Tools" }));
    expect(heading.textContent).toBe("What Cozy SOC can run");
    expect(document.activeElement).toBe(heading);
    fireEvent.click(screen.getByRole("button", { name: "View devices" }));
    expect(heading.textContent).toBe("What Cozy SOC has actually seen");
    expect(document.activeElement).toBe(heading);

    const refresh = screen.getByRole("button", { name: "Refresh evidence" });
    refresh.focus();
    fireEvent.click(refresh);
    expect(await screen.findByText("device.two")).toBeTruthy();
    expect(loadData).toHaveBeenCalledTimes(2);
    expect(document.activeElement).toBe(refresh);
  });

  it("refreshes live evidence on a bounded visible-tab interval without resetting navigation", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      const next = { ...liveData, devices: parseDeviceList({
        configured: true,
        scope_id: "scope.home",
        as_of: "2026-09-10T01:01:00Z",
        devices: [
          ...liveData.devices!.devices,
          { id: "device.two", first_seen: "2026-09-10T01:01:00Z", last_seen: "2026-09-10T01:01:00Z", state: "visible" },
        ],
        truncated: false,
      }) };
      const loadData = vi.fn().mockResolvedValueOnce(liveData).mockResolvedValue(next);
      render(<App loadData={loadData} />);
      await screen.findByRole("status", { name: "Live controller data" });
      fireEvent.click(screen.getByRole("button", { name: "Devices" }));
      expect(screen.getByText("Living Room TV")).toBeTruthy();
      await act(async () => { vi.advanceTimersByTime(60_000); });
      expect(loadData).toHaveBeenCalledTimes(2);
      expect(screen.getByRole("heading", { name: "What Cozy SOC has actually seen" })).toBeTruthy();
      expect(screen.getByText("device.two")).toBeTruthy();
    } finally {
      vi.useRealTimers();
    }
  });

  it("hides stale presence and coverage after a failed refresh and recovers on retry", async () => {
    let rejectRefresh: (error: Error) => void = () => undefined;
    const loadData = vi.fn()
      .mockResolvedValueOnce(liveData)
      .mockImplementationOnce(() => new Promise<AppData>((_resolve, reject) => { rejectRefresh = reject; }))
      .mockResolvedValue(liveData);
    render(<App loadData={loadData} />);
    await screen.findByRole("status", { name: "Live controller data" });
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    fireEvent.click(screen.getByRole("button", { name: "Refresh evidence" }));
    expect(screen.getByText("Living Room TV")).toBeTruthy();
    await waitFor(() => expect(loadData).toHaveBeenCalledTimes(2));
    await act(async () => { rejectRefresh(new Error("private controller diagnostic")); });
    expect(screen.getByRole("alert", { name: "Live evidence out of date" })).toBeTruthy();
    expect(screen.queryByText(/private controller diagnostic/)).toBeNull();
    expect(screen.queryByText("Living Room TV")).toBeNull();
    expect(screen.queryByRole("button", { name: "Rename Living Room TV" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Overview" }));
    expect(screen.queryByRole("button", { name: "Pause Device Watch" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Waiting for fresh evidence" })).toBeTruthy();
    expect(screen.queryByText("1 visible now")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Retry live read" }));
    await screen.findByRole("status", { name: "Live controller data" });
    expect(screen.getByRole("button", { name: "Pause Device Watch" })).toBeTruthy();
  });

  it("does not poll a hidden tab and marks its prior snapshot stale on return", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const visibility = vi.spyOn(document, "visibilityState", "get");
    try {
      let finishRefresh: (data: AppData) => void = () => undefined;
      const loadData = vi.fn().mockResolvedValueOnce(liveData)
        .mockImplementationOnce(() => new Promise<AppData>((resolve) => { finishRefresh = resolve; }));
      render(<App loadData={loadData} />);
      await screen.findByRole("status", { name: "Live controller data" });
      await act(async () => { await Promise.resolve(); });
      visibility.mockReturnValue("hidden");
      await act(async () => { vi.advanceTimersByTime(120_000); });
      expect(loadData).toHaveBeenCalledTimes(1);
      visibility.mockReturnValue("visible");
      fireEvent(document, new Event("visibilitychange"));
      expect(screen.getByRole("alert", { name: "Live evidence out of date" })).toBeTruthy();
      await act(async () => { await Promise.resolve(); });
      await waitFor(() => expect(loadData).toHaveBeenCalledTimes(2));
      await act(async () => { finishRefresh(liveData); });
      expect(screen.getByRole("status", { name: "Live controller data" })).toBeTruthy();
      expect(loadData).toHaveBeenCalledTimes(2);
    } finally {
      visibility.mockRestore();
      vi.useRealTimers();
    }
  });

  it("starts on a live evidence-based overview and navigates to devices, activity, coverage, and tools", async () => {
    render(<App loadData={async () => liveData} />);
    expect(await screen.findByRole("status", { name: "Live controller data" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Your home at a glance" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Device Watch is reporting current evidence" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review coverage evidence" }));
    expect(screen.getByRole("heading", { name: "Know what is visible. Know what is not." })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Overview" }));
    expect(screen.getByText("1 visible now")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Overview" }).getAttribute("aria-current")).toBe("page");

    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByRole("heading", { name: "What Cozy SOC has actually seen" })).toBeTruthy();
    expect(screen.getByText("Living Room TV")).toBeTruthy();
    expect(screen.getByText("Visible now", { selector: ".presence-pill" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(screen.getByRole("heading", { name: "What changed on your visible network" })).toBeTruthy();
    expect(screen.getByText("Observed recently")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Coverage" }));
    expect(screen.getByRole("heading", { name: "Know what is visible. Know what is not." })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "What Cozy SOC can see" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Tools" }));
    expect(screen.getByRole("heading", { name: "What Cozy SOC can run" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Device Watch" })).toBeTruthy();
    expect(screen.getByText("Candidate; release validation is still pending.")).toBeTruthy();
  });

  it("keeps core evidence live when only activity is unavailable", async () => {
    const degraded: AppData = { ...liveData, activity: null, activity_error: "Activity projection is temporarily unavailable." };
    render(<App loadData={async () => degraded} />);
    expect(await screen.findByRole("status", { name: "Live controller data" })).toBeTruthy();
    expect(screen.queryByRole("alert", { name: "Live monitoring unavailable" })).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByText("Living Room TV")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(screen.getByRole("heading", { name: "Activity is temporarily unavailable" })).toBeTruthy();
    expect(screen.getByText("Activity projection is temporarily unavailable.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry activity" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Coverage" }));
    expect(screen.getByRole("heading", { name: "What Cozy SOC can see" })).toBeTruthy();
  });

  it("keeps core evidence live when only tools are unavailable", async () => {
    const degraded: AppData = { ...liveData, tools: null, tools_error: "Capability projection is temporarily unavailable." };
    render(<App loadData={async () => degraded} />);
    expect(await screen.findByRole("status", { name: "Live controller data" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Tools" }));
    expect(screen.getByRole("heading", { name: "Tool information is temporarily unavailable" })).toBeTruthy();
    expect(screen.getByText("Capability projection is temporarily unavailable.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Retry tools" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByText("Living Room TV")).toBeTruthy();
  });

  it("does not silently replace an unavailable controller with demo data", async () => {
    render(<App loadData={async () => Promise.reject(new Error("private offline diagnostic"))} />);
    expect(await screen.findByRole("alert", { name: "Live monitoring unavailable" })).toBeTruthy();
    expect(screen.queryByText(/private offline diagnostic/)).toBeNull();
    expect(screen.queryByRole("status", { name: "Synthetic demo data" })).toBeNull();
    expect(screen.queryByText("Living Room TV")).toBeNull();
  });

  it("keeps the synthetic label visible while navigating demo sections and never exposes live setup controls", async () => {
    render(<App loadData={async () => Promise.reject(new Error("offline"))} />);
    await screen.findByRole("alert", { name: "Live monitoring unavailable" });
    fireEvent.click(screen.getByRole("button", { name: "Use synthetic demo" }));
    expect(screen.getByRole("status", { name: "Synthetic demo data" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Pause Device Watch" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Enable Device Watch" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByText("Living Room TV")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Activity" }));
    expect(screen.getByText("Address changed")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Tools" }));
    expect(screen.getByRole("heading", { name: "Device Watch" })).toBeTruthy();
    expect(screen.getByRole("status", { name: "Synthetic demo data" })).toBeTruthy();
  });
  it("keeps core evidence live when only a requested local sample fails", async () => {
    render(<App loadData={async () => liveData} loadLocalQuality={async () => { throw new Error("private-source-error"); }} />);
    await screen.findByRole("status", { name: "Live controller data" });
    fireEvent.click(screen.getByRole("button", { name: "Read local interface" }));
    expect(await screen.findByText(/Local sample unavailable/)).toBeTruthy();
    expect(screen.queryByText(/private-source-error/)).toBeNull();
    expect(screen.getByRole("status", { name: "Live controller data" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByText("Living Room TV")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Coverage" }));
    expect(screen.getByRole("heading", { name: "What Cozy SOC can see" })).toBeTruthy();
  });

  it("does not take a local sample while entering or navigating the synthetic demo", async () => {
    let reads = 0;
    render(<App loadData={async () => { throw new Error("offline"); }} loadLocalQuality={async () => { reads++; return { enrolled: false, as_of: "2026-09-10T12:00:00Z" }; }} />);
    await screen.findByRole("alert", { name: "Live monitoring unavailable" });
    fireEvent.click(screen.getByRole("button", { name: "Use synthetic demo" }));
    expect(screen.getByText(/Synthetic demo: no local interface sample/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Read local interface" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Tools" }));
    expect(screen.getByRole("status", { name: "Synthetic demo data" })).toBeTruthy();
    expect(reads).toBe(0);
  });
});
