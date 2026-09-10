export const demoStatusRaw = {
  controller_version: "synthetic-demo",
  started_at: "2026-09-09T22:30:00Z",
  config_schema_version: 1,
  transport: "unix",
};

export const demoCapabilitiesRaw = {
  catalog_schema_version: 1,
  capabilities: [{
    id: "device-watch",
    display_name: "Device Watch",
    summary: "Find devices visible from an enrolled home network and track presence without claiming whole-network traffic visibility.",
    release: "v0.1",
    configured: true,
    ownership: "builtin",
    state: { desired: "enabled", process: "not-applicable", verification: "verified" },
    targets: [{ os: "darwin", arch: "arm64", min_version: "13.0", support: "candidate" }],
    privileges: [{ id: "local-network-access", requirement: "conditional", description: "Platform or network policy may require local-network permission; exact behavior remains a tested platform prerequisite." }],
    resources: { measurement: "unmeasured", profile: "desktop-base", evidence: "Foundation resource measurements remain release-gated." },
    provenance: { kind: "first-party", license: "MIT", version_policy: "Ships with the Cozy SOC controller and follows the controller release version." },
    health: { process_required: false, verification_signals: ["network-scope-enrolled", "observation-freshness", "sensor-operational", "ingestion-health", "storage-health"], coverage_requires_verification: true },
    lifecycle: ["preflight", "enable", "verify", "disable"],
    deep_link_count: 0,
  }],
};
