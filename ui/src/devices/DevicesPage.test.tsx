import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { SetupRequestError, type DeviceLabelClient } from "../setup/setup";
import { DevicesPage } from "./DevicesPage";
import { parseDeviceList } from "./devices";

const devices = parseDeviceList({
  configured: true,
  scope_id: "scope.home",
  as_of: "2026-09-10T12:00:00Z",
  devices: [{ id: "device.one", user_label: "Living Room TV", first_seen: "2026-09-09T20:00:00Z", last_seen: "2026-09-10T11:59:00Z", state: "visible" }],
  truncated: false,
});

function client(labelDevice: DeviceLabelClient["labelDevice"]): DeviceLabelClient {
  return { labelDevice };
}

describe("DevicesPage labels", () => {
  it("renames a device and asks the parent to reload live state", async () => {
    const labelDevice = vi.fn(async (deviceID: string, label: string) => ({ device_id: deviceID, user_label: label, changed: true }));
    const onChanged = vi.fn();
    render(<DevicesPage devices={devices} labelClient={client(labelDevice)} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole("button", { name: "Rename Living Room TV" }));
    fireEvent.change(screen.getByLabelText("Device label for Living Room TV"), { target: { value: "Kitchen TV" } });
    fireEvent.click(screen.getByRole("button", { name: "Save label" }));
    await waitFor(() => expect(labelDevice).toHaveBeenCalledWith("device.one", "Kitchen TV"));
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("clears only the label and reloads live state", async () => {
    const labelDevice = vi.fn(async (deviceID: string, label: string) => ({ device_id: deviceID, user_label: label, changed: true }));
    const onChanged = vi.fn();
    render(<DevicesPage devices={devices} labelClient={client(labelDevice)} onChanged={onChanged} />);
    fireEvent.click(screen.getByRole("button", { name: "Clear label" }));
    await waitFor(() => expect(labelDevice).toHaveBeenCalledWith("device.one", ""));
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("keeps a scoped not-found error local to the row", async () => {
    const labelDevice = vi.fn(async () => { throw new SetupRequestError("not_found", "not found", 404); });
    render(<DevicesPage devices={devices} labelClient={client(labelDevice)} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Rename Living Room TV" }));
    fireEvent.change(screen.getByLabelText("Device label for Living Room TV"), { target: { value: "Kitchen TV" } });
    fireEvent.click(screen.getByRole("button", { name: "Save label" }));
    expect((await screen.findByRole("alert")).textContent).toContain("no longer available in the current authorized scope");
  });

  it("blocks untrimmed labels before submission", () => {
    const labelDevice = vi.fn();
    render(<DevicesPage devices={devices} labelClient={client(labelDevice)} onChanged={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Rename Living Room TV" }));
    fireEvent.change(screen.getByLabelText("Device label for Living Room TV"), { target: { value: " Kitchen TV " } });
    expect(screen.getByText("Device labels cannot start or end with whitespace.")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Save label" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("renders hostile label text as text and keeps demo/read-only rows non-mutating", () => {
    const hostile = parseDeviceList({
      configured: true,
      scope_id: "scope.home",
      as_of: "2026-09-10T12:00:00Z",
      devices: [{ id: "device.hostile", user_label: `<img src=x onerror="alert(1)">`, first_seen: "2026-09-09T20:00:00Z", last_seen: "2026-09-10T11:59:00Z", state: "visible" }],
      truncated: false,
    });
    const { container } = render(<DevicesPage devices={hostile} />);
    expect(screen.getByText(`<img src=x onerror="alert(1)">`)).toBeTruthy();
    expect(container.querySelector("img")).toBeNull();
    expect(screen.queryByRole("button", { name: /Rename|Name|Clear label/ })).toBeNull();
  });
});
