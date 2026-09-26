import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ArrivalFindingsPage } from "./ArrivalFindingsPage";

describe("arrival findings page", () => {
  it("shows uncertainty and source expiry without a security verdict", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      as_of: "2026-09-26T17:00:00Z", truncated: false,
      items: [{ id: "finding.one", scope_id: "scope.home", observed_at: "2026-09-26T16:00:00Z", recorded_at: "2026-09-26T16:01:00Z", evidence_observation_id: "obs.one", evidence_retained: false }],
    }), { headers: { "Content-Type": "application/json" } })));
    render(<ArrivalFindingsPage mode="live" onNavigate={() => undefined} />);
    expect(await screen.findByText("New network identity observed")).toBeTruthy();
    expect(screen.getByText(/source observation has expired or is unavailable/i)).toBeTruthy();
    expect(screen.getByText(/not security verdicts or desktop notifications/i)).toBeTruthy();
    expect(screen.getByText(/arrival alone calls for no network action/i)).toBeTruthy();
    vi.unstubAllGlobals();
  });

  it("records a reviewed state through the CSRF-protected route", async () => {
    const calls: string[] = [];
    vi.stubGlobal("fetch", vi.fn((input: string, init?: RequestInit) => {
      calls.push(input);
      const value = input === "/api/session" ? { csrf_token: "c".repeat(43) } : input.endsWith("/acknowledge")
        ? { finding_id: "finding.one", acknowledged_at: "2026-09-26T17:05:00Z", changed: true }
        : { as_of: "2026-09-26T17:00:00Z", truncated: false, items: [{ id: "finding.one", scope_id: "scope.home", observed_at: "2026-09-26T16:00:00Z", recorded_at: "2026-09-26T16:01:00Z", evidence_observation_id: "obs.one", evidence_retained: true }] };
      if (input.endsWith("/acknowledge")) expect(new Headers(init?.headers).get("X-Cozy-CSRF")).toBe("c".repeat(43));
      return Promise.resolve(new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } }));
    }));
    render(<ArrivalFindingsPage mode="live" onNavigate={() => undefined} />);
    fireEvent.click(await screen.findByRole("button", { name: "Mark as reviewed" }));
    expect(await screen.findByText(/this records review only/i)).toBeTruthy();
    expect(calls).toEqual(["/api/findings/arrivals", "/api/session", "/api/findings/arrivals/acknowledge"]);
    vi.unstubAllGlobals();
  });
});
