# Shared coverage and observation-point contract

Issue #12 requires Cozy SOC to explain exactly what each capability can and cannot observe without inventing a universal protection score. Device Watch is the first live producer, but the read-model vocabulary is now capability-independent so later DNS, router, packet, and wireless observation points can describe their own evidence honestly.

This contract is a **derived read model** over configured intent, retained `CoverageSample` evidence, and current operational health. It is not a second evidence store and it is not a security finding.

## Contract shape

A coverage report contains:

- one `capability_id`;
- whether that capability is configured;
- an aggregate state/reason and one actionable next step; and
- zero or more explicit observation points.

Each observation point contains:

- stable point identity and kind;
- the source sensor when known;
- point state/reason and one next step;
- configured, verified, and expected-but-unverified scope dimensions;
- source state;
- directions actually observed;
- the evidence time window and freshness boundary;
- observation cadence; and
- explicit known gaps.

The model contains no percentage field and no implicit denominator for unknown devices, networks, VLANs, channels, or traffic paths.

## Aggregate states

The shared capability/observation-point state vocabulary is:

- `unconfigured`;
- `unavailable`;
- `permission-required`;
- `unverified`;
- `active-limited`;
- `degraded`;
- `stale`; and
- `disconnected`.

A producer may use `permission-required` only when its own evidence can distinguish a permission requirement from a generic source failure. Device Watch does **not** currently make that distinction for ARP/NDP table access, so it continues to report the narrower facts it can prove.

## Scope dimensions

Coverage scope is expressed with typed dimensions rather than a single broad label. The initial vocabulary is:

- `network`;
- `interface`;
- `vlan`;
- `device`;
- `address-family`;
- `wireless-band`; and
- `wireless-channel`.

Every observation point keeps three lists separate:

1. `configured` — scope the user/configuration expects this point to cover;
2. `verified` — dimensions for which current evidence actually establishes coverage; and
3. `expected_unverified` — configured dimensions that are still expected but not currently verified.

An expected-but-unverified dimension must also be configured, and a dimension cannot simultaneously be verified and expected-unverified.

Verified dimensions may also be learned from evidence rather than pre-enumerated. For example, a resolver integration may observe clients that were not individually named when the resolver itself was configured. A producer must still avoid turning that observed set into a claim about every client on the network.

## Source states

A point can report multiple expected sources independently. The shared source states are:

- `expected-unverified`;
- `current`;
- `permission-required`;
- `unavailable`;
- `degraded`;
- `stale`;
- `disconnected`; and
- `unknown`.

`Expected` and `Observed` remain separate facts. This preserves the difference between “we expected this source but have no current evidence” and “the latest trusted evidence explicitly came from this source.”

## Directions

Traffic- or service-oriented observation points may declare only directions they actually observe:

- `ingress`;
- `egress`;
- `east-west`;
- `client-to-service`; and
- `service-to-client`.

An empty directions list is meaningful. Device Watch deliberately reports no observed traffic directions because ARP/NDP neighbor-cache evidence does not establish packet or flow visibility.

Missing directions belong in explicit gaps. A gateway packet sensor can therefore report ingress/egress while retaining `east-west` as `direction-not-observed` when local traffic can bypass the gateway.

## Evidence window and cadence

Every point can carry a time-scoped evidence window:

- `started_at`;
- `ended_at`;
- `fresh_until`; and
- an explicit `has_evidence` flag.

No timestamps are present when no evidence exists. When evidence exists, start/end/freshness must be internally ordered.

Cadence is separate from the evidence window:

- `unknown`;
- `continuous`;
- `periodic` with a positive interval;
- `event-driven`; and
- `hopping` with a positive dwell period.

This distinction is important for wireless monitoring: a radio that hops across channels must report dwell behavior instead of being represented as continuously watching every configured channel.

## Known gaps

Each gap contains:

- stable ID and kind;
- summary and detail;
- one actionable next step;
- optional affected scope dimensions; and
- optional missing directions.

Gaps are first-class because an otherwise current observation point can still be intrinsically limited. `active-limited` is therefore not a synonym for incomplete health; it can mean the point is working exactly as designed while known observation boundaries remain.

## Current live producer: Device Watch

Device Watch is currently the only production producer of this shared contract.

The existing authenticated `device-watch.coverage` response remains backward compatible and keeps its Device-Watch-specific fields. It now also includes a nested `coverage` report built from the same existing evidence and operational evaluators. The legacy top-level aggregate state/reason/next step are copied from that validated shared report so there is not a second independent aggregate evaluator.

The Device Watch observation point is:

- ID: `device-watch.local-neighbor-cache`;
- kind: `host-neighbor-cache`;
- configured network plus expected IPv4/IPv6 dimensions;
- enrolled interface when that fact is present in the trusted Device Watch report;
- IPv4/IPv6 verified independently from current ARP/NDP source evidence;
- periodic one-minute cadence;
- no observed traffic directions; and
- curated gaps for host-neighbor-cache incompleteness, isolated segments, and absent traffic visibility.

For current Device Watch coverage samples the shared window uses the trusted snapshot time as both start and end. This is intentionally narrower than inventing an observation interval the source did not establish.

Operational degradation does not erase evidence facts. For example, if current ARP/NDP evidence verifies IPv4/IPv6 but durable ingestion becomes measurably lagging, the point becomes `degraded` while those latest verified scope dimensions remain visible underneath.

## Representability fixtures are not support claims

The shared contract has deterministic fixtures for future observation patterns solely to prove that the schema can express #12 requirements before those capabilities exist.

The fixtures cover:

- **DNS resolver:** configured clients A/B/C/D, observed/verified clients A/B/C, client D expected-unverified, `client-to-service` direction, and an alternate/encrypted-resolver bypass gap;
- **gateway packet sensor:** network/interface/VLAN scope with ingress and egress observed while east-west remains an explicit gap;
- **wireless radio:** band plus channels 1/6/11, hopping cadence with explicit dwell, intermittent channel gaps, and encrypted-content limitations; and
- **permission-required source:** a producer that can explicitly prove permission is the blocker.

These fixtures do **not** mean DNS Protection, Traffic Watch, router visibility, or Wireless Watch are implemented or supported. Actual producers still require their roadmap implementation, integration validation, failure tests, and #29 hardware/workload evidence.

## Validation and trust boundary

The shared internal contract validates bounded counts and text, known state/dimension/direction/cadence vocabularies, unique point/source/gap identities, coherent evidence windows, and configured/verified/expected-unverified invariants.

Observation-point, source, gap, and reason identifiers are bounded token strings so reviewed future producers can introduce precise kinds without making arbitrary executable behavior part of the contract.

The contract carries no commands, executable hooks, arbitrary filesystem queries, credentials, enforcement authority, or mutation surface. Stored free-form evidence text does not become authoritative UI guidance merely because it exists in a `CoverageSample`; each producer remains responsible for validating source-specific evidence and emitting curated gaps/next steps.

## What remains

This shared vocabulary does not complete #12. Remaining work includes:

- real DNS/router/traffic/wireless producers and their source-specific validation;
- source-specific permission evidence where a producer can prove it;
- aggregation rules once a capability has more than one real observation point;
- a generic frontend renderer for the shared contract;
- named-workload calibration for operational thresholds; and
- #29 owned-lab/hardware evidence for observation and failure behavior that deterministic fixtures cannot certify.
