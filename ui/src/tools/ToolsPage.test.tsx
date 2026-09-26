import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { demoCapabilitiesRaw, demoStatusRaw } from "../demo/tools";
import { parseToolsSnapshot } from "./tools";
import { ToolsPage } from "./ToolsPage";
import { parseStorageOverview } from "./storage";

describe("ToolsPage", () => {
  it("keeps candidate and unmeasured claims explicit", () => {
    render(<ToolsPage tools={parseToolsSnapshot(demoStatusRaw, demoCapabilitiesRaw)} onNavigate={() => undefined} />);
    expect(screen.getByRole("heading", { name: "Local Cozy SOC controller" })).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Device Watch" })).toBeTruthy();
    expect(screen.getByText("Candidate; release validation is still pending.")).toBeTruthy();
    expect(screen.getByText("Not measured yet")).toBeTruthy();
    expect(screen.getByText("Built into Cozy SOC")).toBeTruthy();
    expect(screen.getAllByText("Verification passed").length).toBeGreaterThan(0);
    expect(screen.getByText("Coverage is still evaluated separately from this lifecycle state.")).toBeTruthy();
    expect(screen.getByText(/no separate engine UI/i)).toBeTruthy();
    expect(screen.getByRole("heading", { name: "What this capability collects" })).toBeTruthy();
    expect(screen.getByText("Collection starts only after network enrollment and separate Device Watch enablement.")).toBeTruthy();
    expect(screen.getByText("Packet payloads and browsing history.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Enable Device Watch" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Disable Device Watch" })).toBeNull();
    expect(screen.queryByText(/fully protected/i)).toBeNull();
  });

  it("routes Device Watch back to internal evidence views", () => {
    const navigate = vi.fn();
    render(<ToolsPage tools={parseToolsSnapshot(demoStatusRaw, demoCapabilitiesRaw)} onNavigate={navigate} />);
    fireEvent.click(screen.getByRole("button", { name: "View devices" }));
    fireEvent.click(screen.getByRole("button", { name: "View activity" }));
    fireEvent.click(screen.getByRole("button", { name: "View coverage" }));
    expect(navigate.mock.calls.map(([page]) => page)).toEqual(["devices", "activity", "coverage"]);
  });

  it("explains live database usage and retention without treating volume space as quota", () => {
    const storage = parseStorageOverview({
      as_of: "2026-09-25T12:00:00Z", quota_state: "pressure", database_bytes: 80 * 1048576,
      used_bytes: 70 * 1048576, reusable_bytes: 10 * 1048576, max_bytes: 100 * 1048576,
      filesystem_state: "full", filesystem_supported: true, filesystem_total_bytes: 1000 * 1048576,
      filesystem_available_bytes: 0, retention: [
        { class: "ephemeral", duration_seconds: 86400 }, { class: "short", duration_seconds: 7 * 86400 },
        { class: "standard", duration_seconds: 30 * 86400 }, { class: "audit", duration_seconds: 180 * 86400 },
      ],
    });
    render(<ToolsPage tools={parseToolsSnapshot(demoStatusRaw, demoCapabilitiesRaw)} storage={storage} onNavigate={() => undefined} />);
    expect(screen.getByText("70 MiB")).toBeTruthy();
    expect(screen.getByText("100 MiB · Approaching quota")).toBeTruthy();
    expect(screen.getByText("0 MiB available · Volume full")).toBeTruthy();
    expect(screen.getByText("180 days")).toBeTruthy();
  });
});
