import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GatewayCheckPanel } from "./GatewayCheckPanel";
import { parseGatewayCheckResult, parseGatewayCheckReview, type GatewayCheckClient } from "./gateway-check";

const start = new Date("2026-09-25T12:00:00Z");
const review = () => parseGatewayCheckReview({
  review_id: "a".repeat(43), created_at: start.toISOString(), expires_at: new Date(start.getTime() + 30000).toISOString(),
  target: "192.168.50.1", source: "192.168.50.23", interface_name: "en0", interface_index: 4,
  prefixes: ["192.168.50.0/24"], budget: { max_attempts: 3, min_interval_ms: 1000, attempt_timeout_ms: 1000,
    total_timeout_ms: 5000, payload_bytes: 32, max_icmp_request_bytes: 120, max_concurrent_runs: 1, min_run_interval_ms: 60000 },
});
const blocked = () => parseGatewayCheckResult({ outcome: "blocked", run_id: "b".repeat(32), failure_code: "precondition_failed" });
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(start); });
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); });

async function prepare(client: GatewayCheckClient) {
  render(<GatewayCheckPanel mode="live" client={client} />);
  fireEvent.change(screen.getByLabelText("Selected gateway address"), { target: { value: "192.168.50.1" } });
  await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Review one-shot check" })); });
}

describe("gateway check consent panel", () => {
  it("requires a separate approval after showing source, budget, privacy and limits", async () => {
    const client = { reviewGateway: vi.fn(async () => review()), decideGateway: vi.fn(async () => blocked()) };
    await prepare(client);
    expect(client.decideGateway).not.toHaveBeenCalled();
    expect(screen.getByText(/192.168.50.23 via en0/)).toBeTruthy();
    expect(screen.getByText(/Up to 3 ICMP echo requests/)).toBeTruthy();
    expect(screen.getByText(/may be visible to the destination/)).toBeTruthy();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Approve one check to 192.168.50.1" })); });
    expect(client.decideGateway).toHaveBeenCalledExactlyOnceWith("a".repeat(43), true);
    expect(screen.getByText(/Check outcome:/)).toBeTruthy();
    expect(screen.getByText(/No usable connectivity measurement/)).toBeTruthy();
  });

  it("disables approval when review expires or focus is lost", async () => {
    const client = { reviewGateway: vi.fn(async () => review()), decideGateway: vi.fn(async () => blocked()) };
    await prepare(client);
    await act(async () => { vi.advanceTimersByTime(30000); });
    expect((screen.getByRole("button", { name: "Approve one check to 192.168.50.1" }) as HTMLButtonElement).disabled).toBe(true);
    expect(client.decideGateway).not.toHaveBeenCalled();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Decline check" })); });
    expect(client.decideGateway).toHaveBeenCalledExactlyOnceWith("a".repeat(43), false);
  });

  it("shows uncertain outcome after an approval error without retrying or exposing diagnostics", async () => {
    const client = { reviewGateway: vi.fn(async () => review()), decideGateway: vi.fn(async () => { throw new Error("private diagnostic"); }) };
    await prepare(client);
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Approve one check to 192.168.50.1" })); });
    expect(screen.getByRole("alert").textContent).toContain("Traffic may have been sent");
    expect(screen.queryByText(/private diagnostic/)).toBeNull();
    expect(client.decideGateway).toHaveBeenCalledTimes(1);
  });

  it("never exposes an approval control in synthetic demo", () => {
    render(<GatewayCheckPanel mode="demo" client={{ reviewGateway: async () => review(), decideGateway: async () => blocked() }} />);
    expect(screen.queryByRole("button")).toBeNull();
  });
});
