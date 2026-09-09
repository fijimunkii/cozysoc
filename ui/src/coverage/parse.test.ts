import { describe, expect, it } from "vitest";

import { demoCoverageRaw } from "../demo/coverage";
import { parseCoverageReport } from "./parse";

describe("parseCoverageReport", () => {
  it("parses the shared contract and drops unknown score-like fields", () => {
    const raw = structuredClone(demoCoverageRaw) as Record<string, unknown>;
    raw.protection_percent = 100;

    const report = parseCoverageReport(raw);

    expect(report.capability_id).toBe("device-watch");
    expect(report.state).toBe("active-limited");
    expect("protection_percent" in report).toBe(false);
  });

  it("rejects states outside the shared coverage vocabulary", () => {
    const raw = structuredClone(demoCoverageRaw) as Record<string, unknown>;
    raw.state = "fully-protected";

    expect(() => parseCoverageReport(raw)).toThrow(/unsupported value/);
  });

  it("rejects a single-point aggregate that disagrees with its point", () => {
    const raw = structuredClone(demoCoverageRaw) as {
      observation_points: Array<Record<string, unknown>>;
    };
    const point = raw.observation_points[0];
    if (point === undefined) throw new Error("demo point missing");
    point.state = "degraded";

    expect(() => parseCoverageReport(raw)).toThrow(/aggregate must match/);
  });

  it("accepts a truthful unconfigured report with no observation points", () => {
    const report = parseCoverageReport({
      capability_id: "device-watch",
      configured: false,
      state: "unconfigured",
      reason: "not-configured",
      observation_points: [],
      next_step: "Enroll a home network before evaluating coverage.",
    });

    expect(report.configured).toBe(false);
    expect(report.observation_points).toEqual([]);
  });
});
