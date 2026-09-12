import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { HTTPSHistoryPanel } from "./HTTPSHistoryPanel";
import { type HTTPSHistory, type HTTPSEvidence } from "./https-history";
import { httpsHistoryFixture } from "./https-history-fixtures.test-helper";

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });
async function read(name = "Read HTTPS history") { await act(async () => fireEvent.click(screen.getByRole("button", { name }))); }
describe("retained history panel", () => {
  it("reads manually, keeps original times and never treats refresh or focus as another check", async () => {
    const load = vi.fn(async () => httpsHistoryFixture());
    const { container } = render(<HTTPSHistoryPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled(); await read();
    expect(screen.getByText(/Historical records only/)).toBeTruthy();
    expect(container.querySelector('time[datetime="2026-09-12T11:59:56Z"]')).toBeTruthy();
    expect(screen.getByText(/0.000 ms/)).toBeTruthy();
    expect(screen.getByText(/Completed · separate from HTTP status/)).toBeTruthy();
    act(() => { vi.advanceTimersByTime(120000); window.dispatchEvent(new Event("focus")); });
    expect(load).toHaveBeenCalledTimes(1);
    await read("Refresh HTTPS history list"); expect(load).toHaveBeenCalledTimes(2);
    expect(container.querySelector('time[datetime="2026-09-12T11:59:56Z"]')).toBeTruthy();
    expect(screen.queryByRole("button", { name: /run|approve|enable/i })).toBeNull();
  });
  it("keeps negative replies, incomplete and missing evidence distinct", async () => {
    for (const [evidence, title] of [["unknown", "Outcome unknown"], ["incomplete", "Incomplete exchange"], ["timeout", "Phase timed out"], ["redirect-response", "HTTP 301 response"], ["tls-failure", "TLS verification or handshake failed"]] satisfies [HTTPSEvidence, string][]) {
      render(<HTTPSHistoryPanel mode="live" load={async () => httpsHistoryFixture(evidence)} />); await read();
      expect(screen.getByText(`HEAD · ${title}`)).toBeTruthy();
      if (evidence === "redirect-response") expect(screen.getByText("Differed from the recorded expectation")).toBeTruthy();
      expect(screen.queryByText(/ICMP reply loss/)).toBeNull(); cleanup();
    }
  });
  it("separates a received error status from failed execution and missing timing", async () => {
    const value = httpsHistoryFixture(); const run = value.runs[0]!;
    run.measurement!.status_code = 503; run.expectation_matched = false; run.outcome = "failed";
    render(<HTTPSHistoryPanel mode="live" load={async () => value} />); await read();
    expect(screen.getByText("HEAD · HTTP 503 response")).toBeTruthy();
    expect(screen.getByText("Failed · separate from HTTP status")).toBeTruthy();
    expect(screen.getByText("Differed from the recorded expectation")).toBeTruthy();
    cleanup();
    render(<HTTPSHistoryPanel mode="live" load={async () => httpsHistoryFixture("tls-failure")} />); await read();
    expect(screen.getByText(/HTTP request not sent; connection or TLS traffic may have occurred/)).toBeTruthy();
    expect(screen.queryByText(/0.000 ms/)).toBeNull();
  });
  it("explains empty and unenrolled history and both truncation limits", async () => {
    const value = httpsHistoryFixture(); value.runs = []; value.truncated = true; value.scan_truncated = true;
    const load = vi.fn(async () => value);
    render(<HTTPSHistoryPanel mode="live" load={load} />); await read();
    expect(screen.getByText(/No retained HTTPS checks were found/)).toBeTruthy();
    expect(screen.getByRole("note").textContent).toContain("20-run limit");
    expect(screen.getByRole("note").textContent).toContain("record read limit");
    value.enrolled = false; value.truncated = false; value.scan_truncated = false;
    await read("Refresh HTTPS history list"); expect(screen.getByText(/No network was enrolled/)).toBeTruthy();
    expect(screen.queryByText(/No retained HTTPS checks were found/)).toBeNull();
  });
  it("contains errors and retries only the history read", async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error("private-secret")).mockResolvedValue(httpsHistoryFixture());
    render(<HTTPSHistoryPanel mode="live" load={load} />); await read();
    expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/private-secret/)).toBeNull();
    await read("Retry HTTPS history read"); expect(screen.getByText(/HTTP 204 response/)).toBeTruthy();
  });
  it("bounds a hanging request, blocks duplicate reads, and ignores its late result", async () => {
    let resolve!: (value: HTTPSHistory) => void, signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<HTTPSHistory>((r) => { resolve = r; }); });
    render(<HTTPSHistoryPanel mode="live" load={load} />); await read(); await read();
    expect(load).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(6000)); expect(signal.aborted).toBe(true);
    await act(async () => resolve(httpsHistoryFixture()));
    expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/HTTP 204 response/)).toBeNull();
  });
  it("aborts on demo transition and starts clean when live returns", async () => {
    let resolve!: (value: HTTPSHistory) => void, signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<HTTPSHistory>((r) => { resolve = r; }); });
    const { rerender } = render(<HTTPSHistoryPanel mode="live" load={load} />); await read();
    rerender(<HTTPSHistoryPanel mode="demo" load={load} />); expect(signal.aborted).toBe(true);
    await act(async () => resolve(httpsHistoryFixture())); expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText(/Synthetic demo: retained HTTPS history is not loaded/)).toBeTruthy();
    rerender(<HTTPSHistoryPanel mode="live" load={load} />); expect(screen.getByText(/History has not been read yet/)).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });
});
