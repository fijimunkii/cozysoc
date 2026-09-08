# Support and capability matrix

**Research date:** 2026-09-08

This document separates framework/platform possibility from **Cozy SOC product support**. Nothing in this matrix is a release claim until the required real-system evidence exists.

## Status vocabulary

- **Candidate** — preferred P0 validation target; not yet supported.
- **Planned** — intended later; no current support claim.
- **Tested** — passed the named hardware/software acceptance matrix. No entry has this state yet.
- **Limited** — tested but with explicit missing capabilities.
- **Unsupported / unvalidated** — no promise.

## Platform matrix

| Platform | Desktop UI | Controller | Hub / advanced sensor | Current architecture status | Promotion gate |
| --- | --- | --- | --- | --- | --- |
| macOS 13+ Apple Silicon | Candidate | Candidate | Not a reference sensor | First desktop-alpha candidate because macOS 13+ provides the modern `SMAppService` path for bundled launch agents/daemons. Exact OS minimum and architecture support remain unvalidated. | #5 service lifetime, packaging, permissions, sleep/resume, resource measurements |
| macOS Intel | Planned | Planned | Not a reference sensor | Framework/build possibility is not a support claim. | Explicit hardware/build matrix after alpha |
| Windows 10/11 | Planned | Planned | Not initial reference | Long-running controller should integrate with the Windows Service Control Manager when implemented. | Windows-specific #5/#28 equivalent tests |
| Linux desktop | Planned | Planned | Possible | Separate from headless hub support; desktop WebView/package requirements must be tested. | Platform packaging/UI tests |
| Ubuntu Server 26.04 LTS amd64 | N/A | Candidate hub | Candidate | Current Linux hub/sensor reference candidate. Ubuntu 26.04 LTS is a supported LTS release through 2031. | #5 real packet/interface/service tests and #6 engine compatibility |
| Ubuntu Server 26.04 LTS arm64 | N/A | Candidate hub | Candidate | Important candidate for compact home hardware, but not considered supported from architecture compatibility alone. | #5 real arm64 hardware/USB/capture tests and #6 engine compatibility |
| NAS/router appliances and other distros | N/A | Unvalidated | Unvalidated | Do not assume support because containers or Go binaries can run there. | Separate compatibility issue and hardware evidence |

### Platform-source notes

- Apple documents `SMAppService` for registering app-bundled login items, launch agents, and launch daemons on macOS 13 and later: <https://developer.apple.com/documentation/servicemanagement/smappservice>.
- Microsoft documents Windows Services and the Service Control Manager as the mechanism for long-running background services: <https://learn.microsoft.com/en-us/windows/win32/services/about-services>.
- Canonical lists Ubuntu 26.04 LTS as released in April 2026 with standard support into 2031: <https://ubuntu.com/about/release-cycle>.

## Observation capability matrix

| Capability | Base desktop can provide | Deeper prerequisite | Main blind spots that must remain visible |
| --- | --- | --- | --- |
| **Device Watch / discovery** | Candidate: local neighbor/service observations and explicitly bounded authorized probes | Router/inventory integrations can enrich identity and additional subnets | Client isolation, other VLANs/subnets, sleeping controller, changing/private addresses, broadcast/multicast restrictions |
| **Network quality** | Candidate: measurements from the desktop/interface to local gateway/resolver and optional external targets | Router/AP data can add other observation points | One desktop location is not a whole-home RF map; one failed probe is not proof the internet is down |
| **DNS observation / filtering** | No whole-home DNS coverage by default | Observed/managed resolver such as AdGuard Home plus actual clients configured to use it | Encrypted DNS bypass, alternate resolvers, IPv6 configuration gaps, devices outside resolver path |
| **Host traffic observation** | Candidate for the desktop's own traffic where platform permissions allow | None for host-only scope | Says nothing about conversations between other devices |
| **Whole-path / multi-device packet observation** | No, not from ordinary switched desktop attachment | Validated gateway/bridge/mirror/tap/sensor path that actually receives relevant packets | East-west traffic outside observation point, asymmetric/missing mirror directions, encrypted contents, dropped packets |
| **IDS / Traffic Watch** | No meaningful whole-home claim without packet visibility | Validated packet observation plus maintained IDS engine/rules | Same capture gaps as above; alert absence is not safety |
| **Wireless Watch** | No generic support from the built-in client radio | Tested dedicated USB radio + driver/firmware + monitor-mode path, initially on Linux sensor | One radio does not continuously observe every channel; hopping has dwell gaps; passive capture does not guarantee plaintext/decryption |
| **Router state / controls** | No generic router visibility | Explicit supported router API and least-privilege credentials | API-specific data may omit local flows; read capability does not imply enforcement authority |
| **Enforcement** | None merely because a finding exists | Explicit write-capable supported router/resolver/control point + separate authorization | IPv6/alternate paths, stale device identity, partial application, loss of management path |
| **Endpoint posture** | Only the local host data explicitly exposed by the selected platform | Optional endpoint agent/integration | Network discovery is not endpoint coverage |

## Promotion rule

A capability moves from Candidate to Tested only when the implementation records:

1. exact OS/architecture and relevant hardware/driver versions;
2. the observation point and authorized scope;
3. positive evidence that expected data arrives;
4. negative/failure tests showing missing data degrades coverage correctly;
5. measured resource behavior; and
6. uninstall/recovery behavior where the capability changes host/network state.

Successful process startup, successful compilation, or an upstream project's support statement is insufficient on its own.
