import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DeviceDetailPanel } from "./DeviceDetailPanel";
import { parseDeviceDetail } from "./detail";

function detail(state: "visible" | "uncertain") {
  return parseDeviceDetail({
    scope_id: "scope.home", as_of: "2026-09-10T13:00:00Z", truncated: false,
    device: { id: "device.one", user_label: "<img src=x onerror=alert(1)>", first_seen: "2026-09-10T12:00:00Z", last_seen: state === "visible" ? "2026-09-10T12:59:00Z" : "2026-09-10T12:40:00Z", state },
    evidence: [
      { kind: "mac", value: "02:00:00:00:00:01", observed_at: "2026-09-10T12:59:00Z", valid_until: "2026-09-10T13:09:00Z", link_valid_until: "2026-09-10T13:09:00Z", current: true, link_confidence: .9, authority: "inferred", reason: "device-watch:new-mac-candidate:mac", source_sensor_id: "sensor.dw", source: { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T12:59:00Z", attribution: "device-watch:arp-cache" } },
      { kind: "ipv6", value: "2001:db8::20", observed_at: "2026-09-10T12:30:00Z", valid_until: "2026-09-10T12:40:00Z", current: false, authority: "inferred", reason: "device-watch:recent-mac-continuity:ip", source_sensor_id: "sensor.dw", source: { observation_id: "obs.two", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T12:30:00Z", attribution: "device-watch:ndp-cache" } },
    ],
  });
}

describe("DeviceDetailPanel", () => {
  it("separates presence, current identity evidence, and historical evidence without safety claims", () => {
    const { container } = render(<DeviceDetailPanel detail={detail("visible")} onBack={() => undefined} />);
    expect(screen.getByText("Recent positive evidence supports visibility")).toBeTruthy();
    expect(screen.getByText("Current identity evidence")).toBeTruthy();
    expect(screen.getByText("Historical identity evidence")).toBeTruthy();
    expect(screen.getByText("ARP neighbor cache")).toBeTruthy();
    expect(screen.getByText("IPv6 neighbor cache (NDP)")).toBeTruthy();
    expect(screen.getByText(/association only, not device safety/)).toBeTruthy();
    expect(screen.getByText("Association valid until")).toBeTruthy();
    expect(container.querySelector("img")).toBeNull();
  });

  it("explains uncertain as lack of recent positive evidence, not offline proof", () => {
    render(<DeviceDetailPanel detail={detail("uncertain")} onBack={() => undefined} />);
    expect(screen.getByText(/not proof the device is offline/)).toBeTruthy();
  });
});
