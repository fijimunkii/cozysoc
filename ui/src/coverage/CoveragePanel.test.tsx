import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

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
    expect(screen.getByLabelText("Recommended next step")).toBeTruthy();
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

  it("keeps accessible headings distinct when point tokens normalize alike", () => {
    const report = structuredClone(parseCoverageReport(demoCoverageRaw));
    const first = report.observation_points[0];
    if (first === undefined) throw new Error("demo point missing");
    first.id = "sensor.one";
    const second = structuredClone(first);
    second.id = "sensor-one";
    report.observation_points.push(second);

    const { container } = render(<CoveragePanel report={report} />);
    const cards = Array.from(container.querySelectorAll("article.observation-card"));
    expect(cards).toHaveLength(2);
    const titleIDs = cards.map((card) => card.getAttribute("aria-labelledby"));
    expect(new Set(titleIDs).size).toBe(2);
    for (const [index, card] of cards.entries()) {
      expect(card.querySelector("h3")?.id).toBe(titleIDs[index]);
    }
  });

  it("opens source recovery guidance when one source needs permission", () => {
    const report = structuredClone(parseCoverageReport(demoCoverageRaw));
    const source = report.observation_points[0]?.sources[1];
    if (source === undefined) throw new Error("demo source missing");
    source.state = "permission-required";
    source.next_step = "Review the local permission for the IPv6 neighbor source.";

    render(<CoveragePanel report={report} />);

    const details = screen.getByText("Technical source status").closest("details");
    expect(details?.open).toBe(true);
    if (details === null) throw new Error("source details missing");
    expect(within(details).getByText(`Next step: ${source.next_step}`)).toBeTruthy();
  });

  it("shows aggregate guidance when multiple observation points disagree", () => {
    const report = structuredClone(parseCoverageReport(demoCoverageRaw));
    const first = report.observation_points[0];
    if (first === undefined) throw new Error("demo point missing");
    const second = structuredClone(first);
    second.id = "device-watch.other-point";
    second.next_step = "Restore the second source.";
    report.observation_points.push(second);
    report.next_step = "Review both observation points before relying on coverage.";

    render(<CoveragePanel report={report} />);

    expect(within(screen.getByLabelText("Overall next step")).getByText(report.next_step)).toBeTruthy();
    expect(screen.getByText(second.next_step)).toBeTruthy();
  });

  it("gives each capability report its own accessible heading", () => {
    const first = parseCoverageReport(demoCoverageRaw);
    const second = { ...first, capability_id: "traffic-watch" };

    const { container } = render(<><CoveragePanel report={first} /><CoveragePanel report={second} /></>);

    const panels = Array.from(container.querySelectorAll("section.coverage-panel"));
    expect(panels).toHaveLength(2);
    const titleIDs = panels.map((panel) => panel.getAttribute("aria-labelledby"));
    expect(new Set(titleIDs).size).toBe(2);
    for (const [index, panel] of panels.entries()) {
      expect(panel.querySelector("h2")?.id).toBe(titleIDs[index]);
    }
  });

  it("does not introduce protection-score language", () => {
    render(<CoveragePanel report={parseCoverageReport(demoCoverageRaw)} />);

    const text = document.body.textContent?.toLowerCase() ?? "";
    expect(text).not.toContain("fully protected");
    expect(text).not.toContain("protection score");
    expect(text).not.toMatch(/\b100%\b/);
  });
});
