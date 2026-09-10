import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { demoCapabilitiesRaw, demoStatusRaw } from "../demo/tools";
import { parseToolsSnapshot } from "./tools";
import { ToolsPage } from "./ToolsPage";

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
});
