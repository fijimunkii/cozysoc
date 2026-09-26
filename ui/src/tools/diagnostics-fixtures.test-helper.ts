export const diagnosticFixture = {
  schema_version: 3, generated_at: "2026-09-25T12:00:00Z",
  controller: { build_version: "dev", config_schema_version: 4, health_state: "degraded", gap_count: 2 },
  modules: [{ id: "device-watch", adapter_build_version: "dev", desired: "enabled", verification: "degraded" }, { id: "adguard-home", adapter_build_version: "dev", desired: "enabled", verification: "unverified" }, { id: "opnsense", adapter_build_version: "dev", desired: "disabled", verification: "unverified" }],
  coverage: [{ capability_id: "device-watch", state: "degraded", failure_category: "sensor" }],
  storage: { read_state: "current", quota_state: "pressure", volume_state: "current" },
};
