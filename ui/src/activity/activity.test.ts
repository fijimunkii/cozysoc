import { describe, expect, it } from "vitest";
import { ActivityLoadError, parseDeviceActivity } from "./activity";

const fixture = {
  configured: true,
  scope_id: "scope.home",
  since: "2026-09-09T12:00:00Z",
  as_of: "2026-09-10T12:00:00Z",
  items: [{
    id: "obs.one", kind: "address-changed", at: "2026-09-10T11:00:00Z", device_id: "device.one", user_label: "TV",
    address_family: "ipv4", address: "192.168.1.20", previous_address: "192.168.1.10", hardware_address: "02:00:00:00:00:01",
    source: { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T11:00:01Z", attribution: "device-watch:arp-cache" },
  }],
  truncated: false,
};

describe("parseDeviceActivity", () => {
  it("parses a bounded address-change contract", () => {
    const parsed = parseDeviceActivity(fixture);
    expect(parsed.items[0]?.previous_address).toBe("192.168.1.10");
    expect(parsed.items[0]?.source.attribution).toBe("device-watch:arp-cache");
  });
  it("rejects invented departure kinds and malformed change evidence", () => {
    expect(() => parseDeviceActivity({ ...fixture, items: [{ ...fixture.items[0], kind: "departed" }] })).toThrow(ActivityLoadError);
    expect(() => parseDeviceActivity({ ...fixture, items: [{ ...fixture.items[0], previous_address: "192.168.1.20" }] })).toThrow(ActivityLoadError);
  });
  it("rejects cross-contract source metadata, out-of-window events, and non-descending order", () => {
    const first = fixture.items[0];
    if (first === undefined) throw new Error("activity fixture is missing its first item");
    expect(() => parseDeviceActivity({ ...fixture, items: [{ ...first, source: { ...first.source, source_stream: "raw-packets" } }] })).toThrow(ActivityLoadError);
    expect(() => parseDeviceActivity({ ...fixture, items: [{ ...first, at: "2026-09-08T11:00:00Z" }] })).toThrow(ActivityLoadError);
    expect(() => parseDeviceActivity({ ...fixture, items: [first, { ...first, id: "obs.two", at: "2026-09-10T11:30:00Z", source: { ...first.source, observation_id: "obs.two" } }] })).toThrow(ActivityLoadError);
  });
});
