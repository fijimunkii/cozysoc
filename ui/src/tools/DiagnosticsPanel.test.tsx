import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { DiagnosticsPanel } from "./DiagnosticsPanel";
import { parseDiagnosticPreview } from "./diagnostics";
import { diagnosticFixture } from "./diagnostics-fixtures.test-helper";

describe("DiagnosticsPanel", () => {
  it("loads only on request and shows the exact downloadable preview", async () => {
    const load = vi.fn().mockResolvedValue(parseDiagnosticPreview(diagnosticFixture));
    render(<DiagnosticsPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled();
    expect(screen.queryByRole("button", { name: "Save preview as JSON" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Preview diagnostics" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Save preview as JSON" })).toBeTruthy());
    expect(load).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("Diagnostic bundle preview").textContent).toContain('"failure_category": "sensor"');
    expect(screen.getByText(/Nothing is uploaded/)).toBeTruthy();
  });

  it("does not read the controller in demo mode", () => {
    const load = vi.fn();
    render(<DiagnosticsPanel mode="demo" load={load} />);
    expect(screen.queryByRole("button", { name: "Preview diagnostics" })).toBeNull();
    expect(load).not.toHaveBeenCalled();
  });
});
