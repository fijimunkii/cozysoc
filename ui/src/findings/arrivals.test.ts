import { describe, expect, it } from "vitest";
import { parseArrivalFindings } from "./arrivals";

const fixture = {
  as_of: "2026-09-26T17:00:00Z", truncated: false,
  items: [{ id: "finding.one", scope_id: "scope.home", observed_at: "2026-09-26T16:00:00Z", recorded_at: "2026-09-26T16:01:00Z", evidence_observation_id: "obs.one", evidence_retained: false, payload: { private: "secret" } }],
};

describe("arrival findings", () => {
  it("keeps only the bounded reviewed projection", () => {
    const parsed = parseArrivalFindings(fixture);
    expect(parsed.items).toHaveLength(1);
    expect(JSON.stringify(parsed)).not.toContain("secret");
    expect(parsed.items[0]?.evidence_retained).toBe(false);
  });

  it("rejects invalid identity, times and count", () => {
    expect(() => parseArrivalFindings({ ...fixture, items: [{ ...fixture.items[0], id: "<script>" }] })).toThrow();
    expect(() => parseArrivalFindings({ ...fixture, items: [{ ...fixture.items[0], recorded_at: "2026-09-27T00:00:00Z" }] })).toThrow();
    expect(() => parseArrivalFindings({ ...fixture, items: Array(101).fill(fixture.items[0]) })).toThrow();
  });
});
