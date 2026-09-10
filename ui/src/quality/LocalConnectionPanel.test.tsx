import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LocalConnectionPanel } from "./LocalConnectionPanel";
import { parseLocalQuality, type LocalQuality } from "./local-quality";
import { qualityAsOf, qualityRaw } from "./fixtures.test-helper";

beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date(qualityAsOf)); });
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });
async function clickRead(name = "Read local interface") {
  await act(async () => { fireEvent.click(screen.getByRole("button", { name })); });
}

describe("Local connection panel", () => {
  it("is user-requested, explains administrative state, and ages evidence without polling", async () => {
    const load = vi.fn(async () => parseLocalQuality(qualityRaw()));
    render(<LocalConnectionPanel mode="live" load={load} />);
    expect(load).not.toHaveBeenCalled();
    await clickRead();
    expect(screen.getByText("Up", { selector: "dd" })).toBeTruthy();
    expect(screen.getByText("Recent sample — not continuous monitoring.")).toBeTruthy();
    expect(screen.getByText(/not that its physical link or internet connection works/)).toBeTruthy();
    await act(async () => { vi.advanceTimersByTime(29999); });
    expect(screen.queryByText(/Historical sample —/)).toBeNull();
    await act(async () => { vi.advanceTimersByTime(1); });
    expect(screen.getByText("Historical sample — refresh to check again.")).toBeTruthy();
    expect(screen.getByText("Last sampled administrative state")).toBeTruthy();
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("keeps Down local, and preserves unknown gaps without fabricated metrics", async () => {
    const raw = qualityRaw(); raw.check.state = "issue-observed"; raw.check.administrative_up = false;
    const load = vi.fn(async () => parseLocalQuality(raw));
    render(<LocalConnectionPanel mode="live" load={load} />);
    await clickRead();
    expect(screen.getByText("Down", { selector: "dd" })).toBeTruthy();
    raw.check.state = "not-measured"; raw.check.confidence = "unknown"; raw.check.gap = "network-changed"; delete raw.check.administrative_up;
    await clickRead("Refresh local sample");
    expect(screen.getByText("Not measured", { selector: "dd" })).toBeTruthy();
    expect(screen.queryByText("Down", { selector: "dd" })).toBeNull();
    expect(screen.getByText(/interface binding no longer matches enrollment/)).toBeTruthy();
    expect(screen.getByText(/Gateway, DNS, internet reachability, signal strength, latency, and packet loss have not been measured/)).toBeTruthy();
  });

  it("explains enrollment without enabling monitoring", async () => {
    render(<LocalConnectionPanel mode="live" load={async () => ({ enrolled: false, as_of: qualityAsOf })} />);
    await clickRead();
    expect(screen.getByText(/No network is enrolled/)).toBeTruthy();
    expect(screen.queryByText("Up", { selector: "dd" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Enable Device Watch" })).toBeNull();
  });

  it("invalidates on focus, page restore and visibility events without refreshing", async () => {
    const load = vi.fn(async () => parseLocalQuality(qualityRaw()));
    render(<LocalConnectionPanel mode="live" load={load} />);
    await clickRead();
    for (const event of ["focus", "pageshow", "visibilitychange"]) {
      act(() => { (event === "visibilitychange" ? document : window).dispatchEvent(new Event(event)); });
      expect(screen.getByText("Historical sample — refresh to check again.")).toBeTruthy();
    }
    expect(load).toHaveBeenCalledTimes(1);
    await clickRead("Refresh local sample");
    expect(screen.getByText("Recent sample — not continuous monitoring.")).toBeTruthy();
  });

  it("contains raw failures and retries only this read", async () => {
    let failed = true;
    const load = vi.fn(async () => { if (failed) throw new Error("private-secret-url"); return parseLocalQuality(qualityRaw()); });
    render(<LocalConnectionPanel mode="live" load={load} />);
    await clickRead();
    expect(screen.getByRole("alert").textContent).toContain("Local sample unavailable");
    expect(screen.queryByText(/private-secret/)).toBeNull();
    failed = false;
    await clickRead("Retry local sample");
    expect(screen.getByText("Up", { selector: "dd" })).toBeTruthy();
  });

  it("bounds hanging reads, suppresses duplicates and ignores late completion", async () => {
    let resolve!: (data: LocalQuality) => void;
    let signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<LocalQuality>((r) => { resolve = r; }); });
    render(<LocalConnectionPanel mode="live" load={load} />);
    await clickRead();
    expect((screen.getByRole("button", { name: "Read local interface" }) as HTMLButtonElement).disabled).toBe(true);
    expect(load).toHaveBeenCalledTimes(1);
    await act(async () => { vi.advanceTimersByTime(6000); });
    expect(signal.aborted).toBe(true);
    expect(screen.getByRole("alert")).toBeTruthy();
    await act(async () => { resolve(parseLocalQuality(qualityRaw())); });
    expect(screen.queryByText("Up", { selector: "dd" })).toBeNull();
  });

  it("aborts when navigating away and never imports late live results into demo", async () => {
    let resolve!: (data: LocalQuality) => void;
    let signal!: AbortSignal;
    const load = vi.fn((s: AbortSignal) => { signal = s; return new Promise<LocalQuality>((r) => { resolve = r; }); });
    const { rerender } = render(<LocalConnectionPanel mode="live" load={load} />);
    await clickRead();
    rerender(<LocalConnectionPanel mode="demo" load={load} />);
    expect(signal.aborted).toBe(true);
    await act(async () => { resolve(parseLocalQuality(qualityRaw())); });
    expect(screen.getByText(/Synthetic demo: no local interface sample/)).toBeTruthy();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByText("Up", { selector: "dd" })).toBeNull();
    expect(load).toHaveBeenCalledTimes(1);
  });
});
