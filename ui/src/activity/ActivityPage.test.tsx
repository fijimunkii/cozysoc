import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ActivityPage } from "./ActivityPage";
import { parseDeviceActivity } from "./activity";

const activity = parseDeviceActivity({
  configured: true, scope_id: "scope.home", since: "2026-09-09T12:00:00Z", as_of: "2026-09-10T12:00:00Z", truncated: false,
  items: [
    { id: "obs.latest", kind: "observed", at: "2026-09-10T11:00:00Z", device_id: "device.one", user_label: "TV", address_family: "ipv4", address: "192.168.1.20", hardware_address: "02:00:00:00:00:01", source: { observation_id: "obs.latest", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T11:00:01Z", attribution: "device-watch:arp-cache" } },
    { id: "obs.change", kind: "address-changed", at: "2026-09-10T10:00:00Z", device_id: "device.one", user_label: "TV", address_family: "ipv4", address: "192.168.1.20", previous_address: "192.168.1.10", hardware_address: "02:00:00:00:00:01", source: { observation_id: "obs.change", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T10:00:01Z", attribution: "device-watch:arp-cache" } },
    { id: "obs.first", kind: "first-observed", at: "2026-09-10T09:00:00Z", device_id: "device.one", user_label: "TV", address_family: "ipv4", address: "192.168.1.10", hardware_address: "02:00:00:00:00:01", source: { observation_id: "obs.first", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T09:00:01Z", attribution: "device-watch:arp-cache" } },
  ],
});

describe("ActivityPage", () => {
  it("separates first observations, address changes, and latest positive evidence", () => {
    render(<ActivityPage activity={activity} />);
    expect(screen.getByText("First observed")).toBeTruthy();
    expect(screen.getByText("Address changed")).toBeTruthy();
    expect(screen.getByText("Observed recently")).toBeTruthy();
    expect(screen.getByText(/changed from 192.168.1.10 to 192.168.1.20/)).toBeTruthy();
    expect(screen.queryByText(/departed|left|offline/i)).toBeNull();
  });
  it("explains quiet activity without claiming safety", () => {
    render(<ActivityPage activity={{ ...activity, items: [] }} />);
    expect(screen.getByText("No positive device activity in this window")).toBeTruthy();
    expect(screen.getByText(/does not mean the network is empty, safe, or fully observed/i)).toBeTruthy();
  });
});
