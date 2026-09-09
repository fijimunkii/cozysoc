# Canonical domain model

This is the shared vocabulary for the controller, UI, integrations, fixtures, and future sensors. Physical storage tables and wire schemas may evolve, but they should map back to these concepts.

## Installation

One authoritative Cozy SOC control domain.

Contains installation identity, schema/config version, controller identity, deployment mode, policy defaults, and references to enrolled scopes/sensors/capabilities.

Normal operation has one authoritative controller. Desktop-to-hub migration transfers that authority rather than creating two primaries.

## NetworkScope

An explicitly enrolled network or observation boundary the user has authorized Cozy SOC to inspect.

A scope is not merely a CIDR. It may include:

- local interface and network identity;
- IPv4/IPv6 prefixes when known;
- VLAN/zone identity from a trusted source;
- approved discovery behavior;
- observation-point relationships; and
- timestamps for enrollment/change.

A route, VPN, interface, or network change can invalidate assumptions and require revalidation.

## Sensor

A specific observation point.

Examples: desktop discovery source, DNS resolver integration, router API, mirrored packet sensor, or wireless radio.

Important fields/concepts include:

- sensor identity and type;
- location/host;
- declared scope;
- capabilities;
- lifecycle ownership;
- software/hardware version evidence;
- health/freshness; and
- last verified observation.

A sensor being reachable does not prove it is seeing the expected traffic.

## CapabilityInstance

One configured capability, such as Device Watch or Traffic Watch, attached to one installation and one or more sensors/scopes.

Keep separate:

- desired configuration;
- ownership (`external`, `managed-local`, `managed-remote`);
- runtime/health state;
- verified coverage state;
- requested privileges;
- resource/retention policy; and
- exact engine/adapter version where applicable.

## Observation

An immutable normalized statement about something a source observed.

Examples: address seen on an interface, DNS query event, connection metadata, IDS alert, wireless advertisement, or sensor health statistic.

Every observation carries enough provenance to answer:

- who/what observed it;
- when the source says it occurred;
- when Cozy SOC ingested it;
- which scope it belongs to;
- source event/version identity where available; and
- how confident the source attribution is.

Observations are facts about evidence, not conclusions about malicious intent.

## Device

A user-facing, temporal identity assembled from **identity claims**, not a permanent synonym for one IP or MAC address.

Claims can include addresses, DHCP/router identity, service names, hardware hints, endpoint agent identity, and user corrections. Each claim has provenance, validity/freshness, and confidence.

Requirements:

- an IP can belong to different devices at different times;
- a device can have several IPv4/IPv6 addresses;
- MAC/private-address behavior can change;
- two networks can reuse identical private addresses;
- user labels are distinct from inferred names; and
- merge/split corrections preserve original evidence.

## CoverageSample

Evidence describing what a sensor/capability could verify about observation scope during a time interval.

Examples:

- discovery source fresh on interface X;
- DNS resolver observed clients A/B/C but not D;
- mirrored packet sensor received both directions for a test flow;
- capture drops exceeded a threshold;
- wireless radio sampled channels 1/6/11 with specific dwell periods.

Coverage is time-scoped and evidence-based. Old data cannot make a currently disconnected sensor green.

## CoverageReport / ObservationPointCoverage

A derived, user-facing read model that combines configured capability intent, current `CoverageSample` evidence, and current sensor/ingestion/storage health. It is not a second stored evidence stream.

A capability coverage report may contain one or more explicit observation points. Each point keeps separate:

- configured scope dimensions;
- scope dimensions currently verified by evidence;
- configured dimensions that remain expected but unverified;
- expected/observed source state;
- directions actually observed;
- evidence window and observation cadence;
- current operational state; and
- known scope/direction/content gaps with one actionable next step.

Initial scope dimensions include network, interface, VLAN, device, address family, wireless band, and wireless channel. Traffic-oriented points can represent ingress, egress, east-west, client-to-service, and service-to-client directions without assuming every direction is present.

Cadence is explicit because observation behavior matters. Continuous packet visibility, periodic polling, event-driven logs, and a wireless radio hopping with finite dwell are different coverage claims even when all are currently healthy.

The read model has no global percentage denominator. Unknown clients, segments, traffic paths, or unsampled wireless time remain unknown/gaps rather than being silently counted as covered.

Device Watch is the first production producer of this shared contract. DNS resolver, gateway packet, wireless hopping, and permission-required fixtures prove representability only; they are not product-support claims until real producers and validation exist.

## Finding

A detector's assessment derived from one or more observations.

A finding contains:

- category/severity;
- confidence;
- detector/rule/version;
- evidence references;
- required coverage assumptions;
- observed facts;
- inferred interpretation;
- known alternative benign explanations; and
- suppression/acknowledgement state where appropriate.

`Finding` does not mean confirmed compromise.

## Incident

A user-facing correlation of related findings/observations.

Correlation must record **why** items were grouped. Uncertain relationships remain separate rather than being merged for a cleaner UI.

An incident answers:

1. what happened;
2. why it might matter;
3. what evidence supports that interpretation;
4. what Cozy SOC cannot establish; and
5. what safe next step is available.

## Action

A proposed or executed operation that changes Cozy SOC or an external control point.

Security findings and actions are deliberately separate.

An Action includes:

- target and current identity evidence;
- required capability/authority;
- previewed desired change;
- consent/audit actor;
- preconditions;
- requested/applied/effective/failed/rolled-back state;
- expiry where appropriate; and
- rollback information.

An Action that affects the network must revalidate target identity and control-point state before execution.

## AuditEvent

A durable record of security-relevant control-plane changes: enrollment, ownership transition, credential replacement, capability enable/disable, suppression, action approval, configuration migration, and authority transfer.

Audit events are not a replacement for operating-system security logs, but they provide the product-level history needed to explain why Cozy SOC changed state.
