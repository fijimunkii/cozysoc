import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { OPNsenseNeighborHistoryPanel } from "./OPNsenseNeighborHistoryPanel";
import { parseOPNsenseNeighborHistory } from "./opnsense-neighbors";

describe("OPNsenseNeighborHistoryPanel", () => {
  it("reads retained source reports only on request and keeps the coverage limit visible", async () => {
    const load = vi.fn().mockResolvedValue(parseOPNsenseNeighborHistory({
      scope_enrolled: true, scope_id: "scope.home", as_of: "2026-09-26T12:01:00Z", truncated: true,
      reports: [{ observation_id: "obs.opnsense.one", captured_at: "2026-09-26T12:00:00Z", address: "192.168.1.8",
        hardware_address: "02:00:00:00:00:08", interface: "lan", family: "ipv4" }],
    }));
    render(<OPNsenseNeighborHistoryPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Read router reports" }));
    await waitFor(() => expect(screen.getByText(/192\.168\.1\.8/)).toBeTruthy());
    expect(screen.getByText(/obs\.opnsense\.one/)).toBeTruthy();
    expect(screen.getByText(/Older reports may be omitted/)).toBeTruthy();
    expect(screen.getByText(/not verified device identity, current presence, or monitoring coverage/)).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("does not probe in demo mode and explains an empty enrolled scope", async () => {
    const load = vi.fn().mockResolvedValue(parseOPNsenseNeighborHistory({ scope_enrolled: false, as_of: "2026-09-26T12:01:00Z", reports: [], truncated: false }));
    const { rerender } = render(<OPNsenseNeighborHistoryPanel mode="demo" load={load} />);
    expect(screen.queryByRole("button", { name: "Read router reports" })).toBeNull();
    expect(load).not.toHaveBeenCalled();
    rerender(<OPNsenseNeighborHistoryPanel mode="live" load={load} />);
    fireEvent.click(screen.getByRole("button", { name: "Read router reports" }));
    await waitFor(() => expect(screen.getByText("No network scope is enrolled for router reports.")).toBeTruthy());
  });

  it("rejects malformed source records", () => {
    expect(() => parseOPNsenseNeighborHistory({ scope_enrolled: true, scope_id: "scope.home", as_of: "2026-09-26T12:01:00Z", truncated: false,
      reports: [{ observation_id: "obs.one", captured_at: "2026-09-26T12:00:00Z", address: "192.168.1.8", hardware_address: "not-a-mac", interface: "lan", family: "ipv4" }] })).toThrow();
  });
});
