import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DevicesPage } from "./DevicesPage";
import { parseDeviceDetail } from "./detail";
import { parseDeviceList } from "./devices";

const devices = parseDeviceList({ configured: true, scope_id: "scope.home", as_of: "2026-09-10T13:00:00Z", truncated: false, devices: [{ id: "device.one", user_label: "Speaker", first_seen: "2026-09-10T12:00:00Z", last_seen: "2026-09-10T12:59:00Z", state: "visible" }] });
const detail = parseDeviceDetail({ scope_id: "scope.home", as_of: "2026-09-10T13:00:00Z", truncated: false, device: devices.devices[0], evidence: [] });

describe("Devices evidence navigation", () => {
  it("loads device detail only when explicitly opened and returns to the list", async () => {
    const loadDetail = vi.fn(async () => detail);
    render(<DevicesPage devices={devices} loadDetail={loadDetail} />);
    expect(loadDetail).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "View evidence" }));
    expect(await screen.findByRole("heading", { name: "Speaker" })).toBeTruthy();
    expect(loadDetail).toHaveBeenCalledWith("device.one");
    fireEvent.click(screen.getByRole("button", { name: "Back to devices" }));
    expect(screen.getByRole("heading", { name: "Visible network identities" })).toBeTruthy();
  });

  it("keeps read-only/demo rows from exposing live evidence fetch controls", () => {
    render(<DevicesPage devices={devices} />);
    expect(screen.queryByRole("button", { name: "View evidence" })).toBeNull();
  });
});
