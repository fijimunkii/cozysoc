# Security Policy

Cozy SOC is security software that may process sensitive household-network data. Vulnerability reports should therefore minimize public disclosure and avoid including unrelated private data.

## Reporting a vulnerability

**Do not open a public GitHub issue containing vulnerability details, credentials, private keys, packet captures, DNS history, internal addresses, hostnames, SSIDs, or other sensitive household information.**

Preferred reporting path:

1. Use GitHub's **Security → Report a vulnerability** private-reporting flow for this repository when it is available.
2. If private vulnerability reporting is unavailable, do not publish technical details. Open a minimal public issue stating only that you need a private reporting channel, or contact the repository owner through an available private GitHub contact method. Wait for a private channel before sharing the vulnerability.

A useful private report includes:

- affected version or commit;
- affected platform and architecture;
- the smallest reproduction necessary;
- expected versus observed behavior;
- security impact and prerequisites;
- whether elevated privileges, local network access, or user interaction are required; and
- sanitized logs or fixtures only when they materially help reproduce the issue.

Please remove or replace real household identifiers whenever synthetic data can demonstrate the problem.

## Security design

The maintained security-design baseline lives in [`docs/security/`](docs/security/README.md):

- the [threat model](docs/security/threat-model.md) defines assets, adversaries, trust boundaries, concrete abuse cases, and residual risks; and
- the [security requirements](docs/security/security-requirements.md) map normative controls to implementation and regression-test owners.

These documents describe required design behavior. They are not claims that unimplemented controls already exist.

## Scope priorities

High-priority security areas include:

- authentication and authorization of the controller, browser UI, and remote sensors;
- privilege boundaries between the desktop renderer, controller, helpers, engines, and network configuration;
- command, HTML, URL, path, SQL, and configuration injection through untrusted network or engine data;
- secret and credential storage;
- update, artifact, rule-feed, and dependency integrity;
- unsafe network configuration changes or failed rollback;
- unauthorized scanning or observation outside enrolled scope;
- cross-user local access and browser-to-localhost attacks;
- SSRF, unsafe redirects, DNS rebinding, and credential forwarding through integrations;
- resource exhaustion through hostile or malformed event streams;
- unintended exposure of packet, DNS, device, endpoint, or diagnostic data; and
- ways a compromised sensor or integration could gain control-plane authority.

## Current project status

Cozy SOC is in **Foundation** architecture, threat-model, feasibility, and integration research. There is no released application yet. Security claims must follow implemented and tested behavior, not roadmap intent.

As release artifacts are introduced, this policy will be expanded with supported-version windows, coordinated disclosure expectations, and release-specific remediation guidance.

## Security design principles

Contributors should assume that:

- devices on the monitored LAN may be hostile;
- hostnames, SSIDs, URLs, protocol metadata, engine logs, and imported integration data are untrusted;
- a browser may attempt to reach localhost services;
- specialist engines or remote sensors may become compromised;
- an enrolled or authenticated data producer is not automatically authorized for control-plane actions;
- a desktop application should not receive root-equivalent authority merely because a helper requires a narrow privileged operation;
- external integration credentials belong only to their explicitly enrolled authority; and
- zero alerts or a healthy process does not prove complete monitoring coverage.

Security-sensitive architecture or capability changes should update the maintained threat model as part of the same review.
