export const demoStatusRaw = {
  controller_version: "synthetic-demo",
  started_at: "2026-09-09T22:30:00Z",
  config_schema_version: 1,
  transport: "unix",
};

export const demoCapabilitiesRaw = {
  catalog_schema_version: 2,
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
    data_handling: {
      activation: "Collection starts only after network enrollment and separate Device Watch enablement.",
      sources: ["Local macOS ARP and IPv6 neighbor caches for the enrolled network interface."],
      stored: ["Observed IP and MAC addresses with source and time.", "Temporal device associations and presence evidence; labels when you add them.", "Coverage, health, and control audit records."],
      excluded: ["Packet payloads and browsing history.", "Discovery probes or scans sent by Device Watch."],
    },
    lifecycle: ["preflight", "enable", "verify", "disable"],
    deep_link_count: 0,
  }],
};
