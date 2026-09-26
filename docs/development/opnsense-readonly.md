# External OPNsense read boundary

Issue #17 selects OPNsense as the first read-only router reference. The Go
candidate targets the exact 26.7.4 API shape. A native, foreground-only
`cozysoc opnsense-connect` command can now enroll an external connection.
`opnsense-status` makes a fresh version read, and
`opnsense-disconnect` disables local intent before removing the protected
credential. `opnsense-collect ENROLLED_SCOPE_ID` permits one separately approved
ARP/NDP read. The Tools page can request a fresh status-only read and open a
validated credential-free link to the router's own admin page. The link is
withheld when the router shares the Cozy SOC web session's host. There is no
browser setup or browser neighbor collection, no background polling,
network-changing operation, or supported-router claim. An owned 26.7.4 lab
must verify response schema, TLS enrollment, effective privileges, failures,
and recovery before the adapter can be listed as supported.

The Tools page can also read the newest 100 retained neighbor observations for
the active enrolled scope. This local, authenticated read makes the captured
address, MAC, interface, address family, time and evidence ID inspectable; it
does not send another router request. A truncated view says so. The rows stay
separate from Device Watch identity and presence and expire under the ephemeral
retention class. A missing or empty view never establishes absence of devices.

The native connection accepts only a private IP-literal HTTPS origin. Setup
asks for foreground approval before reading the API key and secret with terminal
echo disabled. A self-signed router needs an explicitly supplied regular PEM
file (at most 32 KiB); setup shows the SHA-256 of its bytes for independent
review. The controller probes version before saving an enabled connection.
Durable configuration contains the approved origin and an opaque protected
reference only. The API credential and optional PEM are stored together in
the desktop protected store; the stored origin must still match the approved
origin before the credential can be reused. A failed protected-store deletion
leaves the connection durably disabled for a retry. Native IPC requires an
authenticated, verified same-user OS peer and returns fixed, redacted failures.
The status result contains only connected state, approved origin and the exact
supported version. It does not return router hostname or neighbor data.

Before a neighbor read, the native command displays the currently approved
router origin, selected enrolled scope and its prefixes, limit of 256 rows per
address family, privacy impact, and coverage limits. The user must type
`collect <scope-id>` in a foreground terminal for that single read. The request
carries the reviewed origin and exact enrolled interface binding. The
controller checks the active scope and current interface before the read,
holds the connection lock during the read, then checks the scope and interface
again before storage. A changed endpoint, retired scope, changed binding,
failed audit, or missing protected credential blocks collection. The collection
audit stores counts and phase, never neighbor addresses or credentials. If
storage or the completion audit fails, the caller receives a partial/unknown
outcome and must inspect local health before an explicit retry.

Only validated neighbor IP, MAC, interface and address family inside the
selected prefixes enter local ephemeral evidence with a 24-hour retention
target. Out-of-scope rows are counted and discarded. The source kind is
`router-neighbor-reported`, separate from Device Watch's local-neighbor kind;
it does not create verified device identities or a coverage heartbeat. The
native command returns bounded counts and truncation flags, not household
addresses. Router reports may include stale ARP/NDP entries, omit other VLANs
or east-west traffic, and show addresses affected by NAT. A zero-row result
cannot prove absence or complete visibility.

The client accepts one private IP-literal HTTPS origin with no URL credentials,
path, query, or fragment. It uses OS certificate roots plus an optional
explicitly enrolled PEM CA/router certificate. It keeps normal certificate IP
identity checks enabled, ignores proxy environment variables, follows no
redirects, and sends only GET requests to fixed paths. API key/secret bytes
must come from the protected secret boundary; the client never returns them or
upstream response bodies in errors. DNS hostnames and ordinary HTTP are outside
this candidate contract.

`Probe` reads `/api/diagnostics/system/system_information` and accepts only a
reported `OPNsense 26.7.4-<architecture>` first version entry. It projects
only the version, not the router name or other diagnostic fields.
`ReadNeighbors` then reads `/api/diagnostics/interface/get_arp` and
`/api/diagnostics/interface/get_ndp` once each, without reverse-DNS lookup.
Each response is capped at 2 MiB. The client parses at most 256 rows of each
family, reports the upstream row totals and truncation, and projects only
validated IP, MAC, and interface values. It excludes DHCP hostnames,
manufacturer names, interface descriptions, router name, and any other fields.
Malformed selected rows make the whole read unavailable. ARP/NDP entries are
router-reported neighbor state, not verified device identity, current presence,
flow visibility, network coverage, or security findings.

The initial lab credential should have the upstream **System: Deny config
write**, **Lobby: Dashboard**, **Diagnostics: ARP Table**, and **Diagnostics:
NDP Table** privileges, with no all-pages privilege. Dashboard access is needed
for the version endpoint; the other two grants map to the canonical underscore
API paths. The lab must verify this effective privilege set and that attempted
configuration writes are denied on the exact release. Cozy SOC itself never
issues POSTs or configuration writes. Upstream ACL grants are broader than a
single HTTP method, so the lab result must be stated precisely rather than
calling the credential inherently read-only.

The contract comes from the [OPNsense 26.7.4 API controller](https://github.com/opnsense/core/blob/26.7.4/src/opnsense/mvc/app/controllers/OPNsense/Diagnostics/Api/InterfaceController.php),
[ARP](https://github.com/opnsense/core/blob/26.7.4/src/opnsense/scripts/interfaces/list_arp.py)
and [NDP](https://github.com/opnsense/core/blob/26.7.4/src/opnsense/scripts/interfaces/list_ndp.py)
producers, [Diagnostics ACL](https://github.com/opnsense/core/blob/26.7.4/src/opnsense/mvc/app/models/OPNsense/Diagnostics/ACL/ACL.xml),
and [Core ACL](https://github.com/opnsense/core/blob/26.7.4/src/opnsense/mvc/app/models/OPNsense/Core/ACL/ACL.xml).
The upstream `user-config-readonly` bypass affecting earlier releases was
[patched in 26.7.1](https://github.com/opnsense/core/security/advisories/GHSA-vw8q-pqq7-2q7v),
but this does not replace an effective-permission test on an owned instance.
