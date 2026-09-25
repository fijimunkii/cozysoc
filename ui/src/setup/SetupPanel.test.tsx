import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import type { AppData } from "../app-data";
import { parseDeviceActivity } from "../activity/activity";
import { parseCoverageBundle } from "../coverage/bundle";
import { parseDeviceList } from "../devices/devices";
import { demoCoverageRaw } from "../demo/coverage";
import { SetupPanel } from "./SetupPanel";
import type { DeviceWatchControlResult, SetupClient } from "./setup";
import { parseNetworkList, SetupRequestError } from "./setup";

const candidate = { interface_name: "en0", interface_index: 4, prefixes: ["192.168.1.0/24"] };
const unconfiguredCoverage = {
  capability_id: "device-watch",
  configured: false,
  state: "unconfigured",
  reason: "not-configured",
  observation_points: [],
  next_step: "Authorize a home network and enable Device Watch when ready.",
};

function data(options: { enrolled?: boolean; enabled?: boolean; networkError?: string } = {}): AppData {
  const enabled = options.enabled ?? false;
  const enrolled = options.enrolled ?? enabled;
  return {
    coverage: parseCoverageBundle({
      as_of: "2026-09-10T02:00:00Z",
      reports: [enabled ? demoCoverageRaw : unconfiguredCoverage],
    }),
    activity: parseDeviceActivity(enabled ? { configured: true, scope_id: "scope.home", since: "2026-09-09T02:00:00Z", as_of: "2026-09-10T02:00:00Z", items: [], truncated: false } : { configured: false, since: "2026-09-09T02:00:00Z", as_of: "2026-09-10T02:00:00Z", items: [], truncated: false }),
    tools: null,
    devices: parseDeviceList(enabled ? {
      configured: true,
      scope_id: "scope.home",
      as_of: "2026-09-10T02:00:00Z",
      devices: [],
      truncated: false,
    } : {
      configured: false,
      as_of: "2026-09-10T02:00:00Z",
      devices: [],
      truncated: false,
    }),
    networks: parseNetworkList({
      candidates: [candidate, { interface_name: "en1", interface_index: 5, prefixes: ["10.0.0.0/24"] }],
      candidates_truncated: false,
      ...(enrolled ? {
        enrolled: {
          scope_id: "scope.home",
          enrolled_at: "2026-09-10T01:00:00Z",
          interface: candidate,
        },
      } : {}),
    }),
    ...(options.networkError ? { network_error: options.networkError } : {}),
  };
}

function enabledDataWithCoverage(state: "missing" | "unverified" | "degraded"): AppData {
  const current = data({ enabled: true });
  if (state === "missing") {
    return { ...current, coverage: { ...current.coverage, reports: [] } };
  }
  const report = current.coverage.reports[0]!;
  const point = report.observation_points[0]!;
  const waiting = state === "unverified";
  const nextStep = waiting ? "Wait for the first collection." : "Review ingestion health.";
  return {
    ...current,
    coverage: {
      ...current.coverage,
      reports: [{
        ...report,
        state,
        reason: waiting ? "awaiting-evidence" : "ingestion-lag",
        next_step: nextStep,
        observation_points: [{
          ...point,
          state,
          reason: waiting ? "awaiting-evidence" : "ingestion-lag",
          next_step: nextStep,
          scope: waiting ? { ...point.scope, verified: [], expected_unverified: point.scope.configured } : point.scope,
          sources: waiting ? point.sources.map((source) => ({ ...source, state: "expected-unverified" as const, observed: false, next_step: nextStep })) : point.sources,
          window: waiting ? { has_evidence: false } : point.window,
        }],
      }],
    },
  };
}

function control(active: boolean): DeviceWatchControlResult {
  return {
    scope_id: "scope.home",
    changed: true,
    active,
    state: {
      desired: active ? "enabled" : "disabled",
      process: "not-applicable",
      verification: "unverified",
    },
  };
}

function client(overrides: Partial<SetupClient> = {}): SetupClient {
  return {
    enrollNetwork: vi.fn(async () => ({
      scope_id: "scope.home",
      enrolled_at: "2026-09-10T02:00:00Z",
      interface: candidate,
      changed: true,
    })),
    enableDeviceWatch: vi.fn(async () => control(true)),
    disableDeviceWatch: vi.fn(async () => control(false)),
    ...overrides,
  };
}

describe("SetupPanel", () => {
  it("requires an explicit network choice and review before enrollment", async () => {
    const setup = client();
    const changed = vi.fn();
    render(<SetupPanel data={data()} client={setup} onChanged={changed} onReviewCoverage={() => undefined} />);

    const review = screen.getByRole("button", { name: "Review selection" });
    expect(review.hasAttribute("disabled")).toBe(true);

    fireEvent.click(screen.getByRole("radio", { name: /en0/i }));
    expect(review.hasAttribute("disabled")).toBe(false);
    fireEvent.click(review);
    expect(screen.getByRole("heading", { name: "Authorize en0?" })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Authorize en0?" }));
    expect(screen.getByText(/does not start monitoring yet/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Review selection" }));
    fireEvent.click(screen.getByRole("button", { name: "Review selection" }));

    fireEvent.click(screen.getByRole("button", { name: "Authorize this network" }));
    await waitFor(() => expect(setup.enrollNetwork).toHaveBeenCalledWith("en0"));
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("keeps network enrollment and Device Watch enablement as separate steps", async () => {
    const setup = client();
    const changed = vi.fn();
    render(<SetupPanel data={data({ enrolled: true })} client={setup} onChanged={changed} onReviewCoverage={() => undefined} />);

    expect(screen.getByText(/authorized, but monitoring is still off/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Enable Device Watch" }));
    await waitFor(() => expect(setup.enableDeviceWatch).toHaveBeenCalledTimes(1));
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("moves focus to the next setup step after a successful change without stealing it on ordinary refresh", async () => {
    const setup = client();
    const changed = vi.fn();
    const reviewCoverage = vi.fn();
    const { rerender } = render(<SetupPanel data={data()} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} />);
    expect(document.activeElement).not.toBe(screen.getByRole("heading", { name: "Choose the home network to authorize" }));

    fireEvent.click(screen.getByRole("radio", { name: /en0/i }));
    fireEvent.click(screen.getByRole("button", { name: "Review selection" }));
    fireEvent.click(screen.getByRole("button", { name: "Authorize this network" }));
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
    rerender(<SetupPanel data={data({ enrolled: true })} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} />);
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Enable Device Watch when you are ready" }));

    fireEvent.click(screen.getByRole("button", { name: "Enable Device Watch" }));
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(2));
    rerender(<SetupPanel data={data({ enabled: true })} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} />);
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Device Watch is reporting current evidence" }));

    const pause = screen.getByRole("button", { name: "Pause Device Watch" });
    pause.focus();
    rerender(<SetupPanel data={data({ enabled: true })} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} />);
    expect(document.activeElement).toBe(pause);

    fireEvent.click(pause);
    fireEvent.click(screen.getByRole("button", { name: "Disable Device Watch" }));
    await waitFor(() => expect(changed).toHaveBeenCalledTimes(3));
    rerender(<SetupPanel data={data({ enrolled: true })} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} />);
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Enable Device Watch when you are ready" }));
  });

  it("does not move focus when a setup step changes outside a user mutation", () => {
    const setup = client();
    const changed = vi.fn();
    const reviewCoverage = vi.fn();
    const { rerender } = render(<><button type="button">Outside setup</button><SetupPanel data={data({ enrolled: true })} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} /></>);
    const outside = screen.getByRole("button", { name: "Outside setup" });
    outside.focus();
    rerender(<><button type="button">Outside setup</button><SetupPanel data={data({ enabled: true })} client={setup} onChanged={changed} onReviewCoverage={reviewCoverage} /></>);
    expect(document.activeElement).toBe(outside);
  });

  it("explains prerequisite failure without implying monitoring started", async () => {
    const setup = client({
      enableDeviceWatch: vi.fn(async () => {
        throw new SetupRequestError("precondition_failed", "required prerequisites are not satisfied", 412);
      }),
    });
    render(<SetupPanel data={data({ enrolled: true })} client={setup} onChanged={() => undefined} onReviewCoverage={() => undefined} />);

    fireEvent.click(screen.getByRole("button", { name: "Enable Device Watch" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/network remains authorized/i);
    expect(alert.textContent).toMatch(/monitoring was not enabled/i);
  });

  it("requires confirmation before disabling and keeps network authorization explicit", async () => {
    const setup = client();
    const changed = vi.fn();
    render(<SetupPanel data={data({ enabled: true })} client={setup} onChanged={changed} onReviewCoverage={() => undefined} />);

    fireEvent.click(screen.getByRole("button", { name: "Pause Device Watch" }));
    expect(screen.getByText(/keeps the network authorization/i)).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Disable Device Watch?" }));
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Pause Device Watch" }));
    fireEvent.click(screen.getByRole("button", { name: "Pause Device Watch" }));
    fireEvent.click(screen.getByRole("button", { name: "Disable Device Watch" }));
    await waitFor(() => expect(setup.disableDeviceWatch).toHaveBeenCalledTimes(1));
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("supports a session-only pause and resume path", () => {
    render(<SetupPanel data={data()} client={client()} onChanged={() => undefined} onReviewCoverage={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Not now" }));
    expect(screen.getByRole("heading", { name: "Continue when you are ready" })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Continue when you are ready" }));
    fireEvent.click(screen.getByRole("button", { name: "Resume setup" }));
    expect(screen.getByRole("heading", { name: "Choose the home network to authorize" })).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Choose the home network to authorize" }));
  });

  it("keeps evidence usable when setup-specific network enumeration fails", () => {
    const changed = vi.fn();
    render(<SetupPanel data={data({ networkError: "network list unavailable" })} client={client()} onChanged={changed} onReviewCoverage={() => undefined} />);
    expect(screen.getByRole("heading", { name: "Network setup information is unavailable" })).toBeTruthy();
    expect(screen.getByText(/existing device and coverage evidence remains available/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Retry setup information" }));
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("completes setup only with current limited coverage and opens its details", () => {
    const reviewCoverage = vi.fn();
    render(<SetupPanel data={data({ enabled: true })} client={client()} onChanged={() => undefined} onReviewCoverage={reviewCoverage} />);
    expect(screen.getByText("Setup complete")).toBeTruthy();
    expect(screen.getByText("Current limited coverage verified")).toBeTruthy();
    expect(screen.getByText(/not complete network or traffic monitoring/i)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Review coverage evidence" }));
    expect(reviewCoverage).toHaveBeenCalledTimes(1);
  });

  it.each(["missing", "unverified"] as const)("waits for evidence when coverage is %s", (state) => {
    const changed = vi.fn();
    render(<SetupPanel data={enabledDataWithCoverage(state)} client={client()} onChanged={changed} onReviewCoverage={() => undefined} />);
    expect(screen.getByText("Step 3 of 3")).toBeTruthy();
    expect(screen.getByText("Waiting for current coverage evidence")).toBeTruthy();
    expect(screen.queryByText("Setup complete")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Refresh evidence" }));
    expect(changed).toHaveBeenCalledTimes(1);
  });

  it("keeps degraded coverage marked for review after Device Watch was enabled", () => {
    render(<SetupPanel data={enabledDataWithCoverage("degraded")} client={client()} onChanged={() => undefined} onReviewCoverage={() => undefined} />);
    expect(screen.getByText("Coverage needs review")).toBeTruthy();
    expect(screen.getByText("Review ingestion health.")).toBeTruthy();
    expect(screen.queryByText("Setup complete")).toBeNull();
  });
});
