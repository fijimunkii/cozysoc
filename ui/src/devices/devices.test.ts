import { describe, expect, it } from "vitest";

import { parseDeviceList } from "./devices";

const valid = {
  configured: true,
  scope_id: "scope.home",
  as_of: "2026-09-10T01:00:00Z",
  devices: [
    {
      id: "device.one",
      user_label: "Living Room TV",
      first_seen: "2026-09-09T20:00:00Z",
      last_seen: "2026-09-10T00:59:00Z",
      state: "visible",
    },
  ],
  truncated: false,
};

describe("parseDeviceList", () => {
  it("reconstructs the bounded device read model", () => {
    const parsed = parseDeviceList({ ...valid, ignored: "not-authoritative" });
    expect(parsed.devices[0]?.user_label).toBe("Living Room TV");
    expect(parsed).not.toHaveProperty("ignored");
  });

  it("rejects duplicate device identities", () => {
    expect(() => parseDeviceList({ ...valid, devices: [valid.devices[0], valid.devices[0]] })).toThrow(/duplicate device/);
  });

  it("rejects IDs outside the controller domain grammar", () => {
    expect(() => parseDeviceList({ ...valid, scope_id: "Scope.Home" })).toThrow(/scope_id is invalid/);
    expect(() => parseDeviceList({ ...valid, devices: [{ ...valid.devices[0], id: "Device.One" }] })).toThrow(/device id is invalid/);
  });

  it("rejects unsupported presence states", () => {
    expect(() => parseDeviceList({ ...valid, devices: [{ ...valid.devices[0], state: "offline" }] })).toThrow(/presence state/);
  });

  it("keeps unconfigured distinct from an empty configured network", () => {
    expect(parseDeviceList({ configured: false, as_of: valid.as_of, devices: [], truncated: false }).configured).toBe(false);
    expect(() => parseDeviceList({ configured: false, as_of: valid.as_of, devices: valid.devices, truncated: false })).toThrow(/cannot carry devices/);
  });

  it("rejects contradictory temporal evidence", () => {
    expect(() => parseDeviceList({
      ...valid,
      devices: [{ ...valid.devices[0], first_seen: "2026-09-10T01:01:00Z" }],
    })).toThrow(/after last_seen/);
  });
});
