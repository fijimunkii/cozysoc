# Foundation integration evaluation — 2026-09-08

- **Issue:** #6
- **Research snapshot:** 2026-09-08
- **Decision ADR:** [`../adr/0007-integration-engine-strategy.md`](../adr/0007-integration-engine-strategy.md)
- **Threat-model requirements:** [`../security/security-requirements.md`](../security/security-requirements.md)

## Purpose

This document answers a narrow question: **what is the smallest specialist-tool footprint that gives Cozy SOC meaningful new observation capabilities without turning the product into a bundled SIEM distribution?**

This is a technical/product evaluation, not legal advice. License identifiers and upstream distribution statements are recorded as engineering constraints. Any actual redistribution that creates unresolved obligations must be reviewed before release.

## Decision summary

| Capability | Candidate | Snapshot | Foundation decision | Initial ownership mode |
| --- | --- | --- | --- | --- |
| Device discovery | Cozy SOC native adapter | first-party | **Selected for v0.1** | First-party |
| Inventory enrichment | NetAlertX | tag `v26.9.0` | Defer as default; possible existing-instance adapter later | External only initially |
| DNS observation/filtering | AdGuard Home | `v0.107.79` | **Selected for v0.2** | External/read-only first; managed separately gated |
| Router state | OPNsense | 26.7 series; 26.7.1 research target | **Selected first v0.2 router reference** | External/read-only |
| Traffic IDS | Suricata | `8.0.6` | **Selected for v0.3** | Managed or existing Linux sensor, after validation |
| Rich protocol telemetry | Zeek | `8.0.10` | Defer pending measured incremental value | None by default |
| Behavioral analytics | RITA | `v5.1.2` | Defer; too heavy for default home stack | None by default |
| Wireless monitoring | Kismet | stable `2025-09-R1` | **Selected for v0.4** | Managed/external on validated Linux sensor |
| Security tripwire | OpenCanary | `v0.9.9` | Optional v0.5 candidate | Managed/external after isolation tests |
| Endpoint visibility | Wazuh | `v4.14.7` | Optional v0.5 existing-instance integration | External/read-only initially |
| Active service assessment | Nmap | version not selected | Optional user-supplied tool only unless redistribution rights are separately resolved | User-supplied executable |
| Windows capture dependency | Npcap | version not selected | Do not bundle under the ordinary free distribution path | Not selected for base product |

The exact version in a future release is the version that passes that capability's implementation, hardware, security, and update tests. This table is not an evergreen latest-version pointer.

## Cross-cutting evaluation criteria

Every specialist integration is evaluated against the same contract:

- maintained release/update path;
- stable machine-readable API/event boundary;
- exact platform/architecture prerequisites;
- minimum host/network privileges;
- observation-point prerequisites and blind spots;
- external versus managed ownership behavior;
- input-size/rate/failure containment;
- endpoint identity, TLS, redirect, DNS-rebinding, and credential-scoping behavior where network APIs are used;
- expected CPU/RAM/disk/retention cost;
- useful deep links or need for a Cozy SOC-native evidence view;
- update/artifact/rule/feed provenance;
- uninstall/recovery behavior; and
- license/distribution constraints for every artifact Cozy SOC would actually ship or cause to be installed.

Containerization does not remove these requirements. It is a deployment mechanism, not evidence of safe privilege, observation coverage, or license compatibility.

## Inventory — native discovery versus NetAlertX

### NetAlertX research snapshot

Upstream release tag `v26.9.0` was published September 2, 2026. The release includes a breaking plugin-directory move and warns users of the old API to migrate to the new API. The project is GPL-3.0.

Useful integration properties:

- modern REST/GraphQL API surface;
- device, event, setting, and plugin data suitable for inventory enrichment;
- broad existing discovery/import plugins;
- useful own web UI for advanced deep links.

Costs for Cozy SOC's base product:

- its typical Docker deployment uses host networking for layer-2 discovery;
- documented/container configuration requires broad network capabilities for its scanner workload;
- its image currently incorporates tools including Nmap, creating a separate redistribution/compliance question for any Cozy SOC-managed distribution path;
- its baseline footprint is much larger than a small native discovery adapter; and
- API/plugin structure is actively evolving, so coupling the v0.1 identity model to NetAlertX would make an external project part of the product's most fundamental path.

### Decision

**Do not require or manage NetAlertX for v0.1.** Implement the smallest native discovery adapter needed by #11, with the unified Cozy SOC identity model in #10.

A later **external/read-only NetAlertX adapter** may be useful for users who already run it. If added, use the current supported API rather than its database or deprecated API, and keep NetAlertX observations as source-attributed claims rather than authoritative device identity.

Primary references:

- <https://github.com/jokob-sk/NetAlertX/releases>
- <https://docs.netalertx.com/API/>
- <https://github.com/jokob-sk/NetAlertX/blob/main/LICENSE>

## DNS — AdGuard Home

Upstream release `v0.107.79` was published August 18, 2026. The project is GPL-3.0 and publishes platform-specific release artifacts with digests. Its documented `/control` API and API changelog provide a practical machine boundary for status, statistics, query-log, filtering, and configuration operations.

### Why it fits

AdGuard Home creates a genuinely new observation/control point: DNS activity and filtering for clients that actually use the resolver. Cozy SOC should not recreate a DNS proxy/filter engine.

### Initial contract

**v0.2 starts external/read-only.** Cozy SOC validates a user-approved endpoint, authenticates with least privilege available to the product, ingests only the data needed for device/activity views, and leaves lifecycle/configuration ownership with the user.

Managed deployment is a later slice of #16 and remains gated on:

- explicit artifact/version/provenance and third-party notices/source obligations as applicable;
- an always-on host with stable addressing;
- port and resolver conflict checks;
- safe IPv4/IPv6 behavior;
- query-log minimization;
- tested outage policy and rollback; and
- no design that makes a sleeping desktop the household's sole resolver.

Primary references:

- <https://github.com/AdguardTeam/AdGuardHome/releases/tag/v0.107.79>
- <https://github.com/AdguardTeam/AdGuardHome/blob/master/LICENSE.txt>
- <https://github.com/AdguardTeam/AdGuardHome/blob/master/openapi/openapi.yaml>

## Router — OPNsense

OPNsense 26.7 is the current release family in this research snapshot; 26.7.1 is the initial test target for the read-only adapter. OPNsense is BSD-licensed and exposes JSON APIs under `/api/<module>/<controller>/<command>`, with API-key permissions tied to effective privileges.

### Why it is the first reference

- useful read-only diagnostics exist for interfaces, ARP/NDP neighbors, routes, packet-filter state/statistics, and related operational data;
- API keys can be scoped rather than reusing a broad interactive admin session; and
- an explicit router API gives Cozy SOC a clean place to prove its generic integration-security contract before adding many router families.

### Initial contract

**Read-only only.** #17 must demonstrate one exact supported OPNsense configuration/version using least privilege, explicit TLS trust, endpoint binding, schema/error fixtures, and honest NAT/east-west visibility limits. No firewall, DHCP, DNS, routing, or interface writes belong in the first adapter.

Primary references:

- <https://docs.opnsense.org/development/api.html>
- <https://docs.opnsense.org/development/api/core/diagnostics.html>
- <https://opnsense.org/about/legal-notices/>

## Traffic IDS — Suricata

Upstream release `8.0.6` was published July 7, 2026. Suricata is GPL-2.0 and provides signed upstream source releases. EVE JSON is the selected Cozy SOC integration boundary.

### Why it is selected

Suricata provides the capability we actually need for v0.3 without requiring a full SIEM stack:

- IDS alerts;
- flows and directional byte/packet counts;
- DNS/TLS/HTTP and other protocol metadata when observable;
- engine/capture statistics; and
- correlation identifiers such as `flow_id`.

It is primarily a headless telemetry/detection engine, so **Cozy SOC must provide the user-facing evidence view** rather than pretending Suricata contributes a consumer dashboard.

### Deployment decision

Use Suricata only on a validated Linux observation path from #5/#19. Prefer a separately managed package/service or connect to an existing sensor rather than embedding Suricata into the desktop application artifact. Pin an exact tested engine version and separate engine updates from rule-feed updates.

Rule sources are independent artifacts. #20 must record their own license, provenance, revision, validation, activation, rollback, and noise/resource tests.

Primary references:

- <https://github.com/OISF/suricata/releases/tag/suricata-8.0.6>
- <https://docs.suricata.io/en/suricata-8.0.6/output/eve/eve-json-output.html>
- <https://docs.suricata.io/en/suricata-8.0.6/rule-management/suricata-update.html>
- <https://github.com/OISF/suricata/blob/master/LICENSE>

## Protocol telemetry — Zeek

Upstream release `v8.0.10` was published August 19, 2026. Zeek uses a permissive BSD-style license and produces rich structured protocol telemetry.

The August 2026 release fixed multiple high-severity parser/resource-exhaustion issues. That is useful evidence for our threat-model assumption that packet parsers are high-risk untrusted data-processing components and must be kept current and resource-bounded.

### Decision

**Do not run Zeek by default in v0.3.** First implement Suricata EVE ingestion and the small explainable detectors in #21. Add Zeek only if measured detector quality or evidence richness materially improves enough to justify another packet parser, another update path, and additional CPU/RAM/storage.

Primary references:

- <https://github.com/zeek/zeek/releases/tag/v8.0.10>
- <https://github.com/zeek/zeek/blob/master/COPYING>
- <https://docs.zeek.org/>

## Behavioral analytics — RITA

Upstream release `v5.1.2` was published May 7, 2026. RITA is GPL-3.0, consumes Zeek logs, and its current deployment guidance includes Docker/Compose and ClickHouse. Published sizing guidance is far beyond the footprint desired for Cozy SOC's default household stack.

### Decision

**Defer RITA as a default dependency.** Its beaconing, long-connection, DNS-tunneling, and threat-intelligence concepts are useful references for #21, but Cozy SOC should initially implement a small number of explainable detectors over its normalized flow/DNS model.

A future evaluation can compare detector quality and resource cost against RITA on the same sanitized lab corpus. Do not copy RITA implementation code into first-party MIT code without a separate license review.

Primary references:

- <https://github.com/activecm/rita/releases/tag/v5.1.2>
- <https://github.com/activecm/rita/blob/main/LICENSE>
- <https://www.activecountermeasures.com/free-tools/rita/>

## Wireless — Kismet

Kismet's stable release in this snapshot is `2025-09-R1`; the project does not use GitHub Releases as its authoritative stable-release mechanism. Kismet is GPL-2.0-or-later and has first-class Linux Wi-Fi, datasource, and remote-capture support.

### Why it is selected

Kismet solves the hard, specialized part of Wireless Watch: monitor-mode radio management, channel hopping, wireless protocol/device observation, and capture-source health. Reimplementing this would create substantial driver/platform risk for little product differentiation.

### Initial contract

**v0.4 uses Kismet only on exact validated Linux adapter/driver combinations.** A dedicated radio is the default topology. The Cozy SOC adapter should consume bounded metadata through supported APIs/remote-capture contracts rather than coupling to Kismet's internal database.

The compatibility matrix must record USB VID/PID, chipset/revision, firmware, kernel/driver, Kismet version, bands/channels, and actual hop behavior. One hopping radio must never be represented as continuous all-channel coverage.

Primary references:

- <https://www.kismetwireless.net/downloads/>
- <https://www.kismetwireless.net/docs/readme/datasources/wifi-linux/>
- <https://www.kismetwireless.net/docs/readme/datasources/channelhop/>
- <https://www.kismetwireless.net/docs/readme/remotecap/remotecapture/>
- <https://github.com/kismetwireless/kismet/blob/master/LICENSE>

## Optional tripwire — OpenCanary

Upstream release `v0.9.9` was published July 22, 2026. OpenCanary is BSD-3-Clause and is a comparatively small, focused optional dependency.

### Decision

Keep OpenCanary as an **optional v0.5 candidate**, not a core dependency. #24 must prove isolated deployment, service/port-conflict handling, bounded/redacted payload retention, no WAN exposure, and safe teardown before Cozy SOC offers a managed profile.

Primary references:

- <https://github.com/thinkst/opencanary/releases/tag/v0.9.9>
- <https://github.com/thinkst/opencanary/blob/master/LICENSE>

## Optional endpoint visibility — Wazuh

Upstream release `v4.14.7` was published July 30, 2026. Wazuh is a broad endpoint/security platform with substantially more operational footprint and authority than Cozy SOC needs for its core network-security experience.

### Decision

Start, if implemented, with **external/read-only existing-instance integration only** in #26. Normalize a small set of useful endpoint inventory/freshness/FIM/configuration findings. Do not make the Wazuh manager/indexer/dashboard stack a mandatory or default managed deployment.

Any future managed distribution must separately evaluate the exact Wazuh components, their licenses/interpretations, required services, storage, update path, and resource/support burden.

Primary references:

- <https://github.com/wazuh/wazuh/releases/tag/v4.14.7>
- <https://github.com/wazuh/wazuh/blob/master/LICENSE>
- <https://documentation.wazuh.com/current/user-manual/api/index.html>

## Optional active assessment — Nmap and Npcap

Nmap's project license is the Nmap Public Source License (NPSL), not an ordinary permissive license. Nmap's official OEM page states that OEM licensing is the redistribution path for proprietary distributed products. Npcap likewise has separate OEM redistribution licensing.

### Decision

**Cozy SOC does not bundle Nmap or Npcap under the ordinary base distribution.** If #25 chooses Nmap for conservative posture checks, the initial mode is a **user-supplied executable**:

- user installs/licenses it separately;
- Cozy SOC detects an explicit supported version/path;
- only the vetted scan profile is exposed;
- scope is revalidated before execution; and
- absence of Nmap simply means the optional capability is unavailable.

If the project later wants a one-click managed Nmap/Npcap deployment, obtain and review the appropriate redistribution rights before changing this decision.

Primary references:

- <https://nmap.org/npsl/npsl-annotated.html>
- <https://nmap.org/oem/>
- <https://npcap.com/oem/>

## Deep-link and native-view policy

A specialist tool having data does not mean Cozy SOC should embed its admin interface.

- **Cozy SOC-native evidence required:** Suricata alerts/flows and all correlated incidents.
- **Useful optional deep links:** AdGuard Home, OPNsense, Kismet, NetAlertX, and Wazuh when the upstream product offers a meaningful UI and the exact safe URL/context contract is known.
- Deep links are navigation only: no credentials in URLs, no Cozy SOC native privilege passed to third-party pages, and no unsupported iframe assumption.

## Artifact and update policy

For every managed third-party capability, its implementation issue must record:

1. exact tested version and platform/architecture;
2. authoritative download/repository source;
3. digest/signature/provenance mechanism actually verified;
4. third-party notices and applicable source/distribution obligations;
5. dependency/runtime/driver artifacts installed with it;
6. feed/rule/data licenses separately from engine license;
7. required privileges and network listeners;
8. update compatibility and rollback behavior; and
9. clean uninstall ownership boundaries.

An upstream checksum served from the same channel is useful integrity metadata but must not be described as an independent signature unless it actually is one.

## Re-evaluation triggers

Re-run this evaluation for a candidate when:

- upstream changes license or distribution terms;
- a required API is deprecated or materially changes authentication;
- support requires broader privilege, new listeners, or an unrestricted container socket;
- upstream no longer provides a maintained security-update path;
- resource measurements materially exceed Cozy SOC's product budgets;
- a parser/engine repeatedly creates unacceptable stability or security risk;
- a proposed managed mode adds artifacts not covered by the existing review; or
- a new tool can materially reduce complexity or improve detection/coverage.

## Foundation conclusion

The minimum stack is intentionally small:

- **v0.1:** first-party controller/UI/storage/discovery only; no mandatory specialist engine.
- **v0.2:** AdGuard Home external/read-only first, plus one read-only OPNsense adapter.
- **v0.3:** Suricata on a validated Linux traffic sensor.
- **v0.4:** Kismet on a validated Linux wireless sensor.
- **v0.5:** optional OpenCanary, Wazuh existing-instance integration, and user-supplied Nmap posture checks.

Zeek, RITA, managed NetAlertX, and broad endpoint/SIEM stacks remain deferred until evidence shows they improve the product enough to justify their footprint and maintenance surface.
