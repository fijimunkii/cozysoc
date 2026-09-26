import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { DeviceSplitClient, DeviceSplitList } from "../setup/setup";
import { DeviceSplitPanel } from "./DeviceSplitPanel";
import { parseDeviceDetail } from "./detail";
import { parseDeviceList } from "./devices";

const devices = parseDeviceList({ configured: true, scope_id: "scope.home", as_of: "2026-09-10T12:00:00Z", truncated: false, devices: [
  { id: "device.a", first_seen: "2026-09-09T12:00:00Z", last_seen: "2026-09-10T11:59:00Z", state: "visible" },
  { id: "device.b", first_seen: "2026-09-09T13:00:00Z", last_seen: "2026-09-10T11:59:00Z", state: "visible" },
] }).devices;

const detail = parseDeviceDetail({
  scope_id: "scope.home", as_of: "2026-09-10T12:00:00Z", device: devices[0], truncated: false,
  evidence: [
    { kind: "mac", value: "02:00:00:00:00:01", observed_at: "2026-09-10T11:59:00Z", current: true, authority: "inferred", reason: "device-watch:recent-mac-continuity", source_sensor_id: "sensor.home", source: { observation_id: "obs.one", sensor_id: "sensor.home", kind: "device-neighbor-seen", source_stream: "arp", ingested_at: "2026-09-10T11:59:00Z", attribution: "device-watch:arp-cache" } },
    { kind: "ipv4", value: "192.168.50.10", observed_at: "2026-09-10T11:59:00Z", current: true, authority: "inferred", reason: "device-watch:recent-mac-continuity", source_sensor_id: "sensor.home", source: { observation_id: "obs.one", sensor_id: "sensor.home", kind: "device-neighbor-seen", source_stream: "arp", ingested_at: "2026-09-10T11:59:00Z", attribution: "device-watch:arp-cache" } },
  ],
});

function client(splitObservation = vi.fn(async (sourceID: string, observationID: string, targetID?: string) => ({ source_device_id: sourceID, observation_id: observationID, target_device_id: targetID ?? "device.created", changed: true })), unsplitObservation = vi.fn(async (observationID: string) => ({ observation_id: observationID, changed: true }))): DeviceSplitClient {
  return { splitObservation, unsplitObservation };
}

describe("DeviceSplitPanel", () => {
  it("reviews exact retained claims before splitting one observation", async () => {
    const splitObservation = vi.fn(async (sourceID: string, observationID: string) => ({ source_device_id: sourceID, observation_id: observationID, target_device_id: "device.created", changed: true }));
    const onChanged = vi.fn();
    const loadSplits = vi.fn(async (): Promise<DeviceSplitList> => ({ configured: true, scope_id: "scope.home", splits: [] }));
    render(<DeviceSplitPanel scopeID="scope.home" devices={devices} client={client(splitObservation)} loadSplits={loadSplits} loadDetail={async () => detail} onChanged={onChanged} />);
    await screen.findByText("No observations are currently separated.");
    fireEvent.change(screen.getByLabelText("Device with a wrong observation"), { target: { value: "device.a" } });
    await waitFor(() => expect(screen.getByLabelText("Observation to separate")).toBeTruthy());
    fireEvent.change(screen.getByLabelText("Observation to separate"), { target: { value: "obs.one" } });
    fireEvent.click(screen.getByRole("button", { name: "Review split" }));
    expect(splitObservation).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Confirm observation split" }));
    expect(screen.getAllByText(/02:00:00:00:00:01/).length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole("button", { name: "Confirm split" }));
    await waitFor(() => expect(splitObservation).toHaveBeenCalledWith("device.a", "obs.one", undefined));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
  });

  it("requires review before undoing an active split", async () => {
    const unsplitObservation = vi.fn(async (observationID: string) => ({ observation_id: observationID, changed: true }));
    const onChanged = vi.fn();
    render(<DeviceSplitPanel scopeID="scope.home" devices={devices} client={client(undefined, unsplitObservation)} loadSplits={async () => ({ configured: true, scope_id: "scope.home", splits: [{ observation_id: "obs.one", source_device_id: "device.a", target_device_id: "device.b", created_at: "2026-09-10T12:00:00Z" }] })} loadDetail={async () => detail} onChanged={onChanged} />);
    fireEvent.click(await screen.findByRole("button", { name: "Review split undo" }));
    expect(unsplitObservation).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm split undo" }));
    await waitFor(() => expect(unsplitObservation).toHaveBeenCalledWith("obs.one"));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
  });

  it("refuses split listings from another network", async () => {
    const splitObservation = vi.fn();
    render(<DeviceSplitPanel scopeID="scope.home" devices={devices} client={client(splitObservation)} loadSplits={async () => ({ configured: true, scope_id: "scope.other", splits: [] })} loadDetail={async () => detail} onChanged={vi.fn()} />);
    expect((await screen.findByRole("alert")).textContent).toContain("different network");
    expect(splitObservation).not.toHaveBeenCalled();
  });
});
