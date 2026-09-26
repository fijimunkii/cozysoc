import { describe, expect, it } from "vitest";
import { parseDiagnosticPreview } from "./diagnostics";
import { diagnosticFixture } from "./diagnostics-fixtures.test-helper";

describe("diagnostic preview parsing", () => {
  it("keeps only reviewed fields", () => {
    const parsed = parseDiagnosticPreview({ ...diagnosticFixture, private_path: "/Users/name/secret", controller: { ...diagnosticFixture.controller, raw_error: "token=abc" } });
    expect(JSON.stringify(parsed)).not.toContain("secret");
    expect(parsed.coverage[0]?.failure_category).toBe("sensor");
    expect(parsed.modules.map((module) => module.id)).toEqual(["device-watch", "adguard-home", "opnsense"]);
    expect(parsed.storage).toEqual({ read_state: "current", quota_state: "pressure", volume_state: "current" });
  });

  it("rejects unreviewed versions, URLs, and arbitrary failure text", () => {
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, schema_version: 2 })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, controller: { ...diagnosticFixture.controller, build_version: "https://private.invalid/?token=abc" } })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, modules: [...diagnosticFixture.modules, diagnosticFixture.modules[0]] })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, modules: [{ id: "private/router", adapter_build_version: "dev", desired: "enabled", verification: "verified" }] })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, modules: [{ id: "opnsense", adapter_build_version: "v0.0.1", desired: "enabled", verification: "verified" }] })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, coverage: [{ capability_id: "device-watch", state: "degraded", failure_category: "private-path" }] })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, storage: { read_state: "current", quota_state: "/private/path", volume_state: "current" } })).toThrow();
    expect(() => parseDiagnosticPreview({ ...diagnosticFixture, storage: { read_state: "unavailable", quota_state: "current", volume_state: "unknown" } })).toThrow();
  });

  it("keeps a failed storage read explicitly unknown", () => {
    const parsed = parseDiagnosticPreview({
      ...diagnosticFixture,
      storage: { read_state: "unavailable", quota_state: "unknown", volume_state: "unknown" },
    });
    expect(parsed.storage).toEqual({ read_state: "unavailable", quota_state: "unknown", volume_state: "unknown" });
  });
});
