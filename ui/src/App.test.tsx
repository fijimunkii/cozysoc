import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { AppData } from "./app-data";
import { App } from "./App";
import { parseCoverageBundle } from "./coverage/bundle";
import { parseDeviceList } from "./devices/devices";
import { demoCoverageRaw } from "./demo/coverage";
import { parseNetworkList } from "./setup/setup";

const liveData: AppData = {
  coverage: parseCoverageBundle({ as_of: "2026-09-10T01:00:00Z", reports: [demoCoverageRaw] }),
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
  it("starts on a live evidence-based overview and navigates to devices and coverage", async () => {
    render(<App loadData={async () => liveData} />);
    expect(await screen.findByRole("status", { name: "Live controller data" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Your home at a glance" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Monitoring is enabled for en0" })).toBeTruthy();
    expect(screen.getByText("1 visible now")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Overview" }).getAttribute("aria-current")).toBe("page");

    fireEvent.click(screen.getByRole("button", { name: "Devices" }));
    expect(screen.getByRole("heading", { name: "What Cozy SOC has actually seen" })).toBeTruthy();
    expect(screen.getByText("Living Room TV")).toBeTruthy();
    expect(screen.getByText("Visible now", { selector: ".presence-pill" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Coverage" }));
    expect(screen.getByRole("heading", { name: "Know what is visible. Know what is not." })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "What Cozy SOC can see" })).toBeTruthy();
  });

  it("does not silently replace an unavailable controller with demo data", async () => {
    render(<App loadData={async () => Promise.reject(new Error("offline"))} />);
    expect(await screen.findByRole("alert", { name: "Live monitoring unavailable" })).toBeTruthy();
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
    expect(screen.getByRole("status", { name: "Synthetic demo data" })).toBeTruthy();
  });
});
