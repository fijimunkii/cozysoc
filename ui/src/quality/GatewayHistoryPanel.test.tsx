import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GatewayHistoryPanel } from "./GatewayHistoryPanel";
import { type GatewayHistory, type HistoryEvidence } from "./gateway-history";
import { historyFixture } from "./history-fixtures.test-helper";

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function read(name = "Read gateway history") { await act(async () => fireEvent.click(screen.getByRole("button", { name }))); }
describe("retained history panel", () => {
  it("reads manually, keeps original times and never treats refresh or focus as another check", async () => {
    const load = vi.fn(async () => historyFixture());
    const { container } = render(<GatewayHistoryPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled(); await read();
    expect(screen.getByText(/Historical records only/)).toBeTruthy();
    expect(container.querySelector('time[datetime="2026-09-12T11:59:56Z"]')).toBeTruthy();
    expect(screen.getByText("0.000 ms")).toBeTruthy();
    expect(screen.getByText(/Completed · separate from connectivity/)).toBeTruthy();
    act(() => { vi.advanceTimersByTime(120000); window.dispatchEvent(new Event("focus")); });
    expect(load).toHaveBeenCalledTimes(1);
    await read("Refresh history list"); expect(load).toHaveBeenCalledTimes(2);
    expect(container.querySelector('time[datetime="2026-09-12T11:59:56Z"]')).toBeTruthy();
    expect(screen.queryByRole("button", { name: /run|approve|enable/i })).toBeNull();
  });
  it("previews the exact local export, saves it without rereading, and clears the preview on refresh", async () => {
    const first = historyFixture(); first.truncated = true;
    const second = historyFixture(); second.runs = [];
    const load = vi.fn().mockResolvedValueOnce(first).mockResolvedValueOnce(second);
    const createObjectURL = vi.fn(() => "blob:local-export");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", class ExportURL extends URL {
      static createObjectURL = createObjectURL;
      static revokeObjectURL = revokeObjectURL;
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    render(<GatewayHistoryPanel mode="live" load={load} />);
    expect(screen.queryByRole("button", { name: "Review gateway history JSON" })).toBeNull();
    await read();
    fireEvent.click(screen.getByRole("button", { name: "Review gateway history JSON" }));
    const preview = screen.getByLabelText("gateway history JSON preview").textContent!;
    expect(JSON.parse(preview)).toEqual({ format: "cozysoc-gateway-history", version: 1, snapshot: first });
    fireEvent.click(screen.getByRole("button", { name: "Save gateway history JSON" }));
    expect(load).toHaveBeenCalledTimes(1);
    expect(click).toHaveBeenCalledTimes(1);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(await (createObjectURL.mock.calls[0] as unknown as [Blob])[0].text()).toBe(preview);
    await read("Refresh history list");
    expect(screen.queryByRole("button", { name: "Save gateway history JSON" })).toBeNull();
    expect(load).toHaveBeenCalledTimes(2);
  });
  it("keeps no replies, incomplete, missing and legacy evidence distinct", async () => {
    for (const [evidence, title] of [
      ["missing-terminal", "Outcome unknown"], ["execution-only", "Execution record only"], ["no-measurement", "No usable measurement"],
      ["incomplete", "Incomplete sample"], ["no-replies", "No requests answered"], ["some-replies", "Some requests answered"],
    ] satisfies [HistoryEvidence, string][]) {
      render(<GatewayHistoryPanel mode="live" load={async () => historyFixture(evidence)} />); await read();
      expect(screen.getByText(`192.168.50.1 · ${title}`)).toBeTruthy();
      if (["missing-terminal", "execution-only", "no-measurement", "incomplete"].includes(evidence)) expect(screen.queryByText("ICMP reply loss at sample time")).toBeNull();
      if (evidence === "incomplete") expect(screen.getByText(/Unsent requests are not timeouts/)).toBeTruthy();
      if (evidence === "no-replies") {
        expect(screen.getByText(/One target's silence does not prove an internet outage/)).toBeTruthy();
        expect(screen.getByText("Unknown", { selector: "dd" })).toBeTruthy();
      }
      cleanup();
    }
  });
  it("explains empty and unenrolled history and both truncation limits", async () => {
    const value = historyFixture(); value.runs = []; value.truncated = true; value.scan_truncated = true;
    const load = vi.fn(async () => value);
    render(<GatewayHistoryPanel mode="live" load={load} />); await read();
    expect(screen.getByText(/No retained gateway checks were found/)).toBeTruthy();
    expect(screen.getByRole("note").textContent).toContain("20-run limit");
    expect(screen.getByRole("note").textContent).toContain("record read limit");
    value.enrolled = false; value.truncated = false; value.scan_truncated = false;
    await read("Refresh history list"); expect(screen.getByText(/No network was enrolled/)).toBeTruthy();
    expect(screen.queryByText(/No retained gateway checks were found/)).toBeNull();
  });
  it("contains errors and retries only the history read", async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error("private-secret")).mockResolvedValue(historyFixture());
    render(<GatewayHistoryPanel mode="live" load={load} />); await read();
    expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/private-secret/)).toBeNull();
    await read("Retry history read"); expect(screen.getByText(/All three requests answered/)).toBeTruthy();
  });
  it("bounds a hanging request, blocks duplicate reads, and ignores its late result", async () => {
    let resolve!: (value: GatewayHistory) => void, signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<GatewayHistory>((r) => { resolve = r; }); });
    render(<GatewayHistoryPanel mode="live" load={load} />); await read(); await read();
    expect(load).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(6000)); expect(signal.aborted).toBe(true);
    await act(async () => resolve(historyFixture()));
    expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/All three requests answered/)).toBeNull();
  });
  it("aborts on demo transition and starts clean when live returns", async () => {
    let resolve!: (value: GatewayHistory) => void, signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<GatewayHistory>((r) => { resolve = r; }); });
    const { rerender } = render(<GatewayHistoryPanel mode="live" load={load} />); await read();
    rerender(<GatewayHistoryPanel mode="demo" load={load} />); expect(signal.aborted).toBe(true);
    await act(async () => resolve(historyFixture())); expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText(/Synthetic demo: retained gateway history is not loaded/)).toBeTruthy();
    rerender(<GatewayHistoryPanel mode="live" load={load} />); expect(screen.getByText(/History has not been read yet/)).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });
});
