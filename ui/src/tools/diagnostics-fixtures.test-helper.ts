export const diagnosticFixture = {
  schema_version: 1, generated_at: "2026-09-25T12:00:00Z",
  controller: { build_version: "dev", config_schema_version: 4, health_state: "degraded", gap_count: 2 },
  modules: [{ id: "device-watch", build_version: "dev", desired: "enabled", verification: "degraded" }],
  coverage: [{ capability_id: "device-watch", state: "degraded", failure_category: "sensor" }],
};
