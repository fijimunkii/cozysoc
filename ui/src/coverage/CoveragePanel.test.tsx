import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { App } from "../App";
import { demoCoverageRaw } from "../demo/coverage";
import { CoveragePanel } from "./CoveragePanel";
import { parseCoverageReport } from "./parse";

describe("CoveragePanel", () => {
  it("keeps configured, verified, and expected-unverified scope visually distinct", () => {
    const report = structuredClone(parseCoverageReport(demoCoverageRaw));
    const point = report.observation_points[0];
    if (point === undefined) throw new Error("demo point missing");
    report.state = "degraded";
    report.reason = "source-partial";
    report.next_step = "Restore IPv6 neighbor evidence if dual-stack visibility matters.";
    point.state = report.state;
    point.reason = report.reason;
    point.next_step = report.next_step;
    point.scope.verified = point.scope.verified.filter(
      (dimension) => !(dimension.kind === "address-family" && dimension.value === "ipv6"),
    );
    point.scope.expected_unverified = [{ kind: "address-family", value: "ipv6" }];

    render(<CoveragePanel report={report} />);

    expect(screen.getByRole("status", { name: "Coverage status: Needs attention" })).toBeTruthy();
    expect(within(screen.getByRole("group", { name: "Verified now" })).getByText("IPV4")).toBeTruthy();
    expect(within(screen.getByRole("group", { name: "Expected, not verified" })).getByText("IPV6")).toBeTruthy();
    expect(screen.getByText("Device Watch does not observe other devices' traffic")).toBeTruthy();
  });

  it("renders network-derived text as text rather than executable markup", () => {
    const report = structuredClone(parseCoverageReport(demoCoverageRaw));
    const point = report.observation_points[0];
    const gap = point?.gaps[0];
    if (gap === undefined) throw new Error("demo gap missing");
    gap.summary = '<img src=x onerror="alert(1)">';

    render(<CoveragePanel report={report} />);

    expect(screen.getByText(gap.summary)).toBeTruthy();
    expect(document.querySelector("img")).toBeNull();
  });

  it("does not introduce protection-score language", () => {
    render(<CoveragePanel report={parseCoverageReport(demoCoverageRaw)} />);

    const text = document.body.textContent?.toLowerCase() ?? "";
    expect(text).not.toContain("fully protected");
    expect(text).not.toContain("protection score");
    expect(text).not.toMatch(/\b100%\b/);
  });

  it("labels fixture mode so demo data cannot be mistaken for live monitoring", () => {
    render(<App />);

    const banner = screen.getByRole("status", { name: "Synthetic demo data" });
    expect(banner.textContent).toContain("This screen is not connected to live monitoring.");
  });
});
