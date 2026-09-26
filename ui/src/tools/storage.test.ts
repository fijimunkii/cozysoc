import { describe, expect, it } from "vitest";
import { parseStorageOverview } from "./storage";

const overview = {
  as_of: "2026-09-25T12:00:00Z", quota_state: "pressure", database_bytes: 80, used_bytes: 70,
  reusable_bytes: 10, max_bytes: 100, filesystem_state: "full", filesystem_supported: true,
  filesystem_total_bytes: 1000, filesystem_available_bytes: 0,
  retention: [
    { class: "ephemeral", duration_seconds: 86400 },
    { class: "short", duration_seconds: 7 * 86400 },
    { class: "standard", duration_seconds: 30 * 86400 },
    { class: "audit", duration_seconds: 180 * 86400 },
  ],
  inventory: { batch_evidence_records: 12, other_observations: 1, identity_claims: 2, coverage_samples: 3, findings: 0, audit_events: 4, saved_check_selections: 1, labeled_devices: 2 },
};

describe("storage overview", () => {
  it("keeps quota and host-volume signals separate", () => {
    const parsed = parseStorageOverview(overview);
    expect(parsed.quota_state).toBe("pressure");
    expect(parsed.filesystem_state).toBe("full");
    expect(parsed.retention[2]?.duration_seconds).toBe(30 * 86400);
    expect(parsed.inventory.batch_evidence_records).toBe(12);
  });

  it("rejects inconsistent page accounting and incomplete retention", () => {
    expect(() => parseStorageOverview({ ...overview, reusable_bytes: 9 })).toThrow();
    expect(() => parseStorageOverview({ ...overview, retention: overview.retention.slice(0, 3) })).toThrow();
    expect(() => parseStorageOverview({ ...overview, filesystem_available_bytes: 1001 })).toThrow();
    expect(() => parseStorageOverview({ ...overview, inventory: { ...overview.inventory, findings: -1 } })).toThrow();
  });
});
