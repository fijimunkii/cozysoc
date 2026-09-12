import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { QualityDiagnosisPanel } from "./QualityDiagnosisPanel";
import type { DiagnosisConclusion, QualityDiagnosis } from "./quality-diagnosis";
import { diagnosisFixture } from "./diagnosis-fixtures.test-helper";
beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });
async function read(name = "Read historical comparison") { await act(async () => fireEvent.click(screen.getByRole("button", { name }))); }
it("reads manually and preserves historical times without probes or focus polling", async () => {
  const load = vi.fn(async () => diagnosisFixture()); const { container } = render(<QualityDiagnosisPanel mode="live" load={load} />);
  expect(load).not.toHaveBeenCalled(); await read();
  expect(screen.getByText("Selected checks met their expectations")).toBeTruthy(); expect(screen.getByText("NXDOMAIN")).toBeTruthy();
  expect(container.querySelector('time[datetime="2026-09-12T11:59:55Z"]')).toBeTruthy();
  expect(screen.getByText("Original comparison time")).toBeTruthy(); expect(screen.getByText("History read at")).toBeTruthy();
  act(() => { vi.advanceTimersByTime(120000); window.dispatchEvent(new Event("focus")); }); expect(load).toHaveBeenCalledTimes(1);
  await read("Refresh comparison"); expect(load).toHaveBeenCalledTimes(2);
  expect(screen.queryByRole("button", { name: /approve|run|enable/i })).toBeNull();
});
it("shows unknowns, context mismatches and cross-layer problems distinctly", async () => {
  for (const [conclusion, title] of [["not-enrolled", "No network selected"], ["history-incomplete", "History is incomplete"], ["latest-run-unmeasured", "Latest result cannot be compared"], ["observation-context-mismatch", "Checks came from different contexts"], ["icmp-misses-with-responses", "ICMP misses alongside DNS replies"], ["problems-across-selected-layers", "Both selected checks recorded problems"]] satisfies [DiagnosisConclusion, string][]) {
    render(<QualityDiagnosisPanel mode="live" load={async () => diagnosisFixture(conclusion)} />); await read(); expect(screen.getByText(title)).toBeTruthy();
    if (conclusion === "history-incomplete") expect(screen.getByRole("note").textContent).toContain("audit read limit");
    if (conclusion === "latest-run-unmeasured") expect(screen.getByText(/An older success has not been substituted/)).toBeTruthy(); cleanup();
  }
});
it("contains errors and retries only the read", async () => {
  const load = vi.fn().mockRejectedValueOnce(new Error("private-secret")).mockResolvedValue(diagnosisFixture());
  render(<QualityDiagnosisPanel mode="live" load={load} />); await read(); expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/private-secret/)).toBeNull();
  await read("Retry comparison read"); expect(screen.getByText("Selected checks met their expectations")).toBeTruthy();
});
it("bounds a hanging read and ignores a late result", async () => {
  let resolve!: (v: QualityDiagnosis) => void, signal!: AbortSignal;
  const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<QualityDiagnosis>(r => { resolve = r; }); });
  render(<QualityDiagnosisPanel mode="live" load={load} />); await read(); await read(); expect(load).toHaveBeenCalledTimes(1);
  act(() => vi.advanceTimersByTime(6000)); expect(signal.aborted).toBe(true); await act(async () => resolve(diagnosisFixture())); expect(screen.getByRole("alert")).toBeTruthy();
});
it("aborts on demo transition and never loads live data in demo", async () => {
  let resolve!: (v: QualityDiagnosis) => void, signal!: AbortSignal;
  const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<QualityDiagnosis>(r => { resolve = r; }); });
  const { rerender } = render(<QualityDiagnosisPanel mode="live" load={load} />); await read();
  rerender(<QualityDiagnosisPanel mode="demo" load={load} />); expect(signal.aborted).toBe(true); await act(async () => resolve(diagnosisFixture()));
  expect(screen.queryByRole("button")).toBeNull(); expect(screen.getByText(/Synthetic demo: historical diagnosis is not loaded/)).toBeTruthy();
  rerender(<QualityDiagnosisPanel mode="live" load={load} />); expect(screen.getByText(/No comparison has been read yet/)).toBeTruthy(); expect(load).toHaveBeenCalledTimes(1);
});
