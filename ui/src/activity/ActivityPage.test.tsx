import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
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
    render(<ActivityPage activity={activity} mode="live" />);
    expect(screen.getByText("First observed")).toBeTruthy();
    expect(screen.getByText("Address changed")).toBeTruthy();
    expect(screen.getByText("Observed recently")).toBeTruthy();
    expect(screen.getByText(/changed from 192.168.1.10 to 192.168.1.20/)).toBeTruthy();
    expect(screen.queryByText(/departed|left|offline/i)).toBeNull();
  });
  it("explains quiet activity without claiming safety", () => {
    render(<ActivityPage activity={{ ...activity, items: [] }} mode="live" />);
    expect(screen.getByText("No positive device activity in this window")).toBeTruthy();
    expect(screen.getByText(/does not mean the network is empty, safe, or fully observed/i)).toBeTruthy();
  });
  it("offers a reviewed snapshot only for live activity and clears it after refresh", () => {
    const { rerender } = render(<ActivityPage activity={activity} mode="live" />);
    expect(screen.queryByRole("button", { name: "Save reviewed JSON" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Review JSON before saving" }));
    expect(screen.getByLabelText("Activity history export preview").textContent).toContain('"format": "cozysoc-device-activity"');
    expect(screen.getByRole("button", { name: "Save reviewed JSON" })).toBeTruthy();
    rerender(<ActivityPage activity={{ ...activity, as_of: "2026-09-10T12:01:00Z" }} mode="live" />);
    expect(screen.queryByRole("button", { name: "Save reviewed JSON" })).toBeNull();
    rerender(<ActivityPage activity={activity} mode="demo" />);
    expect(screen.queryByRole("button", { name: "Review JSON before saving" })).toBeNull();
  });
  it("downloads the exact reviewed JSON to a fixed filename", async () => {
    const createObjectURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:activity");
    const revokeObjectURL = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    try {
      render(<ActivityPage activity={activity} mode="live" />);
      fireEvent.click(screen.getByRole("button", { name: "Review JSON before saving" }));
      fireEvent.click(screen.getByRole("button", { name: "Save reviewed JSON" }));
      expect(createObjectURL).toHaveBeenCalledTimes(1);
      expect(click).toHaveBeenCalledTimes(1);
      await waitFor(() => expect(revokeObjectURL).toHaveBeenCalledWith("blob:activity"));
    } finally {
      createObjectURL.mockRestore();
      revokeObjectURL.mockRestore();
      click.mockRestore();
    }
  });
});
