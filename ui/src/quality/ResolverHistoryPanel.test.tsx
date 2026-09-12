import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ResolverHistoryPanel } from "./ResolverHistoryPanel";
import { type ResolverHistory, type ResolverEvidence } from "./resolver-history";
import { resolverHistoryFixture } from "./resolver-history-fixtures.test-helper";

beforeEach(() => vi.useFakeTimers());
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });
async function read(name = "Read DNS history") { await act(async () => fireEvent.click(screen.getByRole("button", { name }))); }
describe("retained history panel", () => {
  it("reads manually, keeps original times and never treats refresh or focus as another check", async () => {
    const load = vi.fn(async () => resolverHistoryFixture());
    const { container } = render(<ResolverHistoryPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled(); await read();
    expect(screen.getByText(/Historical records only/)).toBeTruthy();
    expect(container.querySelector('time[datetime="2026-09-12T11:59:56Z"]')).toBeTruthy();
    expect(screen.getByText("0.000 ms")).toBeTruthy();
    expect(screen.getByText(/Completed · separate from DNS result/)).toBeTruthy();
    act(() => { vi.advanceTimersByTime(120000); window.dispatchEvent(new Event("focus")); });
    expect(load).toHaveBeenCalledTimes(1);
    await read("Refresh DNS history list"); expect(load).toHaveBeenCalledTimes(2);
    expect(container.querySelector('time[datetime="2026-09-12T11:59:56Z"]')).toBeTruthy();
    expect(screen.queryByRole("button", { name: /run|approve|enable/i })).toBeNull();
  });
  it("keeps negative replies, incomplete and missing evidence distinct", async () => {
    for (const [evidence, title] of [["unknown", "Outcome unknown"], ["incomplete", "Incomplete exchange"], ["timeout", "No matched reply before timeout"], ["nxdomain", "Name does not exist (NXDOMAIN)"], ["refused", "Query refused (REFUSED)"]] satisfies [ResolverEvidence, string][]) {
      render(<ResolverHistoryPanel mode="live" load={async () => resolverHistoryFixture(evidence)} />); await read();
      expect(screen.getByText(`AAAA · ${title}`)).toBeTruthy();
      if (evidence === "nxdomain") expect(screen.getByText("Differed from the recorded expectation")).toBeTruthy();
      expect(screen.queryByText(/ICMP reply loss/)).toBeNull(); cleanup();
    }
  });
  it("explains empty and unenrolled history and both truncation limits", async () => {
    const value = resolverHistoryFixture(); value.runs = []; value.truncated = true; value.scan_truncated = true;
    const load = vi.fn(async () => value);
    render(<ResolverHistoryPanel mode="live" load={load} />); await read();
    expect(screen.getByText(/No retained DNS checks were found/)).toBeTruthy();
    expect(screen.getByRole("note").textContent).toContain("20-run limit");
    expect(screen.getByRole("note").textContent).toContain("record read limit");
    value.enrolled = false; value.truncated = false; value.scan_truncated = false;
    await read("Refresh DNS history list"); expect(screen.getByText(/No network was enrolled/)).toBeTruthy();
    expect(screen.queryByText(/No retained DNS checks were found/)).toBeNull();
  });
  it("contains errors and retries only the history read", async () => {
    const load = vi.fn().mockRejectedValueOnce(new Error("private-secret")).mockResolvedValue(resolverHistoryFixture());
    render(<ResolverHistoryPanel mode="live" load={load} />); await read();
    expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/private-secret/)).toBeNull();
    await read("Retry DNS history read"); expect(screen.getByText(/Answer returned/)).toBeTruthy();
  });
  it("bounds a hanging request, blocks duplicate reads, and ignores its late result", async () => {
    let resolve!: (value: ResolverHistory) => void, signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<ResolverHistory>((r) => { resolve = r; }); });
    render(<ResolverHistoryPanel mode="live" load={load} />); await read(); await read();
    expect(load).toHaveBeenCalledTimes(1);
    act(() => vi.advanceTimersByTime(6000)); expect(signal.aborted).toBe(true);
    await act(async () => resolve(resolverHistoryFixture()));
    expect(screen.getByRole("alert")).toBeTruthy(); expect(screen.queryByText(/Answer returned/)).toBeNull();
  });
  it("aborts on demo transition and starts clean when live returns", async () => {
    let resolve!: (value: ResolverHistory) => void, signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<ResolverHistory>((r) => { resolve = r; }); });
    const { rerender } = render(<ResolverHistoryPanel mode="live" load={load} />); await read();
    rerender(<ResolverHistoryPanel mode="demo" load={load} />); expect(signal.aborted).toBe(true);
    await act(async () => resolve(resolverHistoryFixture())); expect(screen.queryByRole("button")).toBeNull();
    expect(screen.getByText(/Synthetic demo: retained resolver history is not loaded/)).toBeTruthy();
    rerender(<ResolverHistoryPanel mode="live" load={load} />); expect(screen.getByText(/History has not been read yet/)).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });
});
