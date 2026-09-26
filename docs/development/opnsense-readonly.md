# External OPNsense read boundary

Issue #17 selects OPNsense as the first read-only router reference. The current
Go candidate is an internal client for the exact 26.7.4 API shape; it is not
connected to controller configuration, commands, or the browser, and no router
version is yet a Cozy SOC support claim. An owned 26.7.4 lab must verify the
response schema, TLS enrollment, effective privileges, failures and recovery
before the adapter can be exposed or listed as supported.
The controller integration must bind each read to an enrolled network and
revalidate the selected endpoint and scope before taking a sample.

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
