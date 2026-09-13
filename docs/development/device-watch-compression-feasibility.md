# Device Watch evidence compression feasibility

Related: #150, following the [persisted storage attribution](device-watch-storage-workload.md).
This is a test-only representation experiment. Production storage still exceeds
the 25 MiB/day target; no schema, collection cadence or retention policy changes.

## Reproduce

```sh
TMPDIR=/private/tmp COZYSOC_COMPRESSION_EXPERIMENT=1 go test \
  ./internal/controller/devicewatch \
  -run '^TestDeviceWatchCompressionFeasibility$' -count=1 -timeout=5m -v
```

Use an appropriate temporary directory on Linux. Small round-trip and rejection
checks run in ordinary CI. The opt-in experiment generates all 1,440 collections
of 100 stable synthetic IPv4 neighbors, using production observation construction
and reconciliation with an in-memory stable-continuity fixture. It does not read
a real neighbor cache, send traffic, run SQLite, or reproduce production identity
queries. It starts with the first collection, without the storage workload's warm-up.

Each independent batch includes 100 complete generated observations, 200 identity
claims and 200 device links. Every gzip result is decompressed and compared with
the original serialized evidence bytes. No collection, timestamp, validity window,
confidence, provenance field or identity decision is sampled or aggregated.
Device records, coverage, storage expiry bookkeeping, schema/index pages and
controller overhead are excluded. Consequently this is neither a replacement
for the persisted workload nor a storage-budget pass.

## Results

The [machine-readable results](evidence/device-watch-compression-2026-09-13.json)
record source `1c584bb6af3a63b4925073ab2d8c5bf4637ea1c7`, Go 1.27.1,
darwin/arm64, command and all six full-run reports. Raw serialized evidence totals
317,117,440 bytes. Every batch is at most 220,221 uncompressed bytes in this fixture.
The full comparison passed in 21.66 seconds; that elapsed time excludes persistence
and production identity queries and is not a CPU or latency budget measurement.

| gzip setting | Complete IDs (MiB) | Verified derived IDs (MiB) |
| --- | ---: | ---: |
| BestSpeed | 28.975 | 15.174 |
| DefaultCompression | 26.953 | 13.662 |
| BestCompression | 25.556 | 13.345 |

Plain gzip does not fit even the payload within 25 MiB. The derived-ID candidate
leaves meaningful room for storage overhead, but that room is **unproven** until
measured with real persistence and required indexes.

## What the ID encoding changes

The prototype checks that each claim ID exactly matches the existing versioned
MAC/IP derivation from the observation ID and claim value, and that each link ID
matches the versioned derivation from the device ID and claim ID. It also verifies
the link's claim reference. Only then does it omit those IDs and references from
the encoded copy. Decode reconstructs the same IDs; it does not re-run identity
reconciliation or make a new identity decision. All other fields remain verbatim.
The complete reconstructed JSON must equal the original bytes for every batch.

Noncanonical IDs are rejected by this experiment, not silently replaced. Regression
coverage mutates claim IDs, link IDs and link references independently. A production
format must preserve supported noncanonical records, for example through an explicit
full-record encoding, and must freeze these derivation rules under a codec version.
The prototype is not a production decoder or a safe interface for arbitrary input.

## Production prototype requirements

Production use requires a bounded persistent representation that preserves the
existing storage contract. Required properties include:

- Exact original evidence, identifiers, ambiguity and validity windows across old
  and new records. Store original reconciliation outcomes; never infer continuity
  from a compressed range or reconstruct evidence from current network state.
- Explicit codec versions, strict encoded/decoded limits, corrupt/truncated-input
  rejection and preservation of noncanonical supported records.
- Durable acknowledgment without waiting for an entire minute of records to fill
  a batch. Define transactional updates and crash recovery for partial batches.
- Bounded query indexes, pagination, history snapshot isolation, retention expiry
  and quota handling; decoding cannot turn a bounded query into a whole-day scan.
- Transactional migration with failure recovery, mixed-history correctness and an
  explicit old-binary/rollback policy. Do not weaken existing durability to save space.
- A completed unchanged 100-device/1,440-collection persisted rerun that includes
  all database pages, verifies reopen durability and measures the remaining budget.

This evidence supports pursuing that prototype. It does not choose a final schema,
close #150, change the architecture target, or satisfy CPU/RAM and sustained-run gates.
