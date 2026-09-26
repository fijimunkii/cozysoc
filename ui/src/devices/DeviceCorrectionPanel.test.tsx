import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { type DeviceCorrectionClient, type DeviceMergeList } from "../setup/setup";
import { DeviceCorrectionPanel } from "./DeviceCorrectionPanel";
import { parseDeviceList } from "./devices";

const devices = parseDeviceList({ configured: true, scope_id: "scope.home", as_of: "2026-09-10T12:00:00Z", truncated: false, devices: [
  { id: "device.a", user_label: "Earlier speaker", first_seen: "2026-09-09T12:00:00Z", last_seen: "2026-09-10T11:59:00Z", state: "visible" },
  { id: "device.b", user_label: "Speaker", first_seen: "2026-09-09T13:00:00Z", last_seen: "2026-09-10T11:59:00Z", state: "visible" },
] }).devices;

function client(mergeDevices = vi.fn(async (sourceID: string, targetID: string) => ({ source_device_id: sourceID, target_device_id: targetID, changed: true })), unmergeDevice = vi.fn(async (sourceID: string) => ({ source_device_id: sourceID, changed: true }))): DeviceCorrectionClient {
  return { mergeDevices, unmergeDevice };
}

describe("DeviceCorrectionPanel", () => {
  it("requires a separate review before changing a current identity", async () => {
    const mergeDevices = vi.fn(async (sourceID: string, targetID: string) => ({ source_device_id: sourceID, target_device_id: targetID, changed: true }));
    const onChanged = vi.fn();
    const loadMerges = vi.fn(async (): Promise<DeviceMergeList> => ({ configured: true, scope_id: "scope.home", merges: [] }));
    render(<DeviceCorrectionPanel scopeID="scope.home" devices={devices} client={client(mergeDevices)} loadMerges={loadMerges} onChanged={onChanged} />);
    await screen.findByText("No device identities are currently grouped.");
    fireEvent.change(screen.getByLabelText("Earlier device to group"), { target: { value: "device.a" } });
    fireEvent.change(screen.getByLabelText("Device to keep"), { target: { value: "device.b" } });
    fireEvent.click(screen.getByRole("button", { name: "Review merge" }));
    expect(mergeDevices).not.toHaveBeenCalled();
    expect(screen.getByText(/original observations and associations remain intact/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Confirm merge" }));
    await waitFor(() => expect(mergeDevices).toHaveBeenCalledWith("device.a", "device.b"));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
  });

  it("reviews undo against the active mapping", async () => {
    const unmergeDevice = vi.fn(async (sourceID: string) => ({ source_device_id: sourceID, changed: true }));
    const onChanged = vi.fn();
    const loadMerges = vi.fn(async (): Promise<DeviceMergeList> => ({ configured: true, scope_id: "scope.home", merges: [{ source_device_id: "device.a", target_device_id: "device.b", created_at: "2026-09-10T12:00:00Z" }] }));
    render(<DeviceCorrectionPanel scopeID="scope.home" devices={[devices[1]!]} client={client(undefined, unmergeDevice)} loadMerges={loadMerges} onChanged={onChanged} />);
    fireEvent.click(await screen.findByRole("button", { name: "Review undo" }));
    expect(unmergeDevice).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Confirm undo" }));
    await waitFor(() => expect(unmergeDevice).toHaveBeenCalledWith("device.a"));
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
  });

  it("refuses corrections from another scope", async () => {
    const mergeDevices = vi.fn();
    render(<DeviceCorrectionPanel scopeID="scope.home" devices={devices} client={client(mergeDevices)} loadMerges={async () => ({ configured: true, scope_id: "scope.other", merges: [] })} onChanged={vi.fn()} />);
    expect((await screen.findByRole("alert")).textContent).toContain("different network");
    expect(mergeDevices).not.toHaveBeenCalled();
  });
});
