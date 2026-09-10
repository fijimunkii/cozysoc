import { afterEach, describe, expect, it, vi } from "vitest";
import { loadDeviceDetailFromWeb, parseDeviceDetail } from "./detail";

const fixture = {
  scope_id: "scope.home", as_of: "2026-09-10T13:00:00Z", truncated: false,
  device: { id: "device.one", user_label: "Speaker", first_seen: "2026-09-10T12:00:00Z", last_seen: "2026-09-10T12:59:00Z", state: "visible" },
  evidence: [{ kind: "mac", value: "02:00:00:00:00:01", observed_at: "2026-09-10T12:59:00Z", valid_until: "2026-09-10T13:09:00Z", current: true, claim_confidence: .9, link_confidence: .9, link_valid_until: "2026-09-10T13:09:00Z", authority: "inferred", reason: "device-watch:new-mac-candidate:mac", source_sensor_id: "sensor.dw", source: { observation_id: "obs.one", sensor_id: "sensor.dw", kind: "device-neighbor-seen", source_stream: "device-watch-neighbors", ingested_at: "2026-09-10T12:59:00Z", attribution: "device-watch:arp-cache" } }],
};

afterEach(() => vi.unstubAllGlobals());

describe("device detail contract", () => {
  it("parses bounded current and historical identity evidence", () => {
    const parsed = parseDeviceDetail({ ...fixture, evidence: [...fixture.evidence, { ...fixture.evidence[0], kind: "ipv4", value: "192.168.1.20", observed_at: "2026-09-10T12:30:00Z", current: false, valid_until: "2026-09-10T12:58:00Z" }] });
    expect(parsed.device.id).toBe("device.one");
    expect(parsed.evidence.map((item) => item.current)).toEqual([true, false]);
    expect(parsed.evidence[0]?.link_valid_until).toBe("2026-09-10T13:09:00Z");
  });

  it("rejects invalid enum, confidence, future evidence, and oversized collections", () => {
    expect(() => parseDeviceDetail({ ...fixture, evidence: [{ ...fixture.evidence[0], kind: "trust-score" }] })).toThrow();
    expect(() => parseDeviceDetail({ ...fixture, evidence: [{ ...fixture.evidence[0], link_confidence: 2 }] })).toThrow();
    expect(() => parseDeviceDetail({ ...fixture, evidence: [{ ...fixture.evidence[0], observed_at: "2026-09-10T14:00:00Z" }] })).toThrow();
    expect(() => parseDeviceDetail({ ...fixture, evidence: Array.from({ length: 101 }, () => fixture.evidence[0]) })).toThrow();
  });

  it("requests only the device id and maps scoped 404", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(fixture), { status: 200, headers: { "Content-Type": "application/json" } })).mockResolvedValueOnce(new Response(JSON.stringify({ error: "not_found" }), { status: 404, headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    await expect(loadDeviceDetailFromWeb("device.one")).resolves.toMatchObject({ scope_id: "scope.home" });
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe("/api/devices/detail?device_id=device.one");
    await expect(loadDeviceDetailFromWeb("device.one")).rejects.toThrow("current authorized scope");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
