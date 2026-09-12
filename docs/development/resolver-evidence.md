# Selected-resolver evidence contract

Related work: #14 and #29. `networkquality.AssessResolvers` validates and interprets
already-collected normalized DNS evidence. It performs no I/O and is not connected
to the controller, storage, CLI, browser or any DNS sender. The original generic
`Assess` fixture contract remains available for coarse method-level evidence; it
cannot preserve the DNS details represented here and must not replace them in a
future resolver integration.

## Selection and provenance

Every selection explicitly supplies an ID, immutable resolver and query
configuration references, resolver transport address family, UDP/TCP transport,
A/AAAA question type and expected result (answer, NXDOMAIN or NODATA). There is no
default resolver, default query name or fallback destination. The references are
not addresses, names, URLs or executable instructions. Configuration owners must
rotate references when pinned endpoint/name configuration changes; reusing a
mutable reference would destroy historical attribution.

The resolver's transport family is independent of the question type: an AAAA
question sent over IPv4 does not measure IPv6 reachability. Every measurement
carries the full selection and observing scope/sensor/interface. Context must
match exactly. A result for one resolver, query, family or transport cannot fill
missing evidence for another.

Each measurement describes at most one logical request. Request state is
`not-sent`, `accepted`, or `uncertain`; exchange state independently records a
matched response, completed timeout, transport error, incomplete exchange, or
explicit not-measured gap. A timeout requires an accepted request. Neither an
unsent nor an uncertain request becomes a timeout or authorizes a retry.

## Protocol semantics

A `DNSReply` is a collector's normalized account of a matched response, not a wire
parser or proof of authenticity. A future collector must validate endpoint,
transaction, question and bounded message structure before supplying it.

- A response code is retained independently of request/transport state. REFUSED,
  SERVFAIL, FORMERR and NOTIMP remain distinct; other error codes remain bounded
  numeric evidence with conservative guidance.
- NOERROR requires an explicit answer, NODATA, referral or unclassified disposition.
  It alone is not successful resolution. A positive disposition requires an
  answer for the selected question, not an unrelated resource record.
- NXDOMAIN and NODATA are negative responses, not timeouts. Their interpretation
  depends on the selected query expectation. An expected negative can pass that
  particular check; a differing answer is not automatically a broken resolver.
- Truncation takes precedence over a conclusive lookup interpretation. Truncated
  replies cannot carry answer classifications. No TCP fallback is performed.
- Referrals and unclassified responses leave the lookup outcome unknown. No
  additional server or alias query is performed.

These distinctions follow [RFC 1035's response header semantics](https://www.rfc-editor.org/rfc/rfc1035.html#section-4.1.1)
and [RFC 2308's NODATA/referral distinction](https://www.rfc-editor.org/rfc/rfc2308.html#section-2.2).
NODATA needs response-content classification, not an empty-answer-count heuristic.
The bounded 0–4095 code representation can retain the combined
[EDNS extended RCODE](https://www.rfc-editor.org/rfc/rfc6891.html#section-6.1.3);
this does not implement or advertise EDNS support.

Response time is optional, preserves measured zero and describes a matched reply,
including error replies. It is not successful-lookup latency. A timeout,
incomplete exchange or transport error cannot carry response timing. No DNS
result produces packet loss, an internet-down verdict, DNSSEC assurance, a
security finding or monitoring coverage.

## Evidence time and bounds

Snapshots reuse the representation limits of the generic contract: 16 selections,
128 measurements, a 24-hour window, one second to fifteen minutes of freshness,
and at most thirty seconds per attempted exchange. Explicit no-request gaps may
span the assessment window. Timestamps must round-trip through signed nanoseconds.
These are input limits, not sender traffic budgets or scheduler guarantees.

The latest completion per selection wins regardless of input order. A newer
explicit gap supersedes earlier success. Duplicate IDs, tied completion times,
context changes, unknown enums and contradictory metrics fail closed without
copying source-controlled strings into errors.

At the exact freshness deadline, the selected evidence is historical. The result
code and original reply/timing remain available in a defensive evidence copy;
current response timing is absent and confidence is unknown. Missing evidence
has no invented time, gap cause, or metric. Recent confidence is limited to the
selected response/attempt; an incomplete exchange has unknown confidence.

## Required next boundaries

This contract adds no resolver execution support. A bounded collector still needs
explicit resolver/query selection and disclosure, immutable endpoint/name
binding, enrollment and route/source checks, wire validation, cancellation,
byte/request/deadline/retry limits, cooldown/concurrency control, durable audit,
retention and one-shot consent. It must not fall back through host DNS, an
unselected public resolver, referrals or additional alias queries silently.
The OS resolver API cannot be treated as a selected-endpoint measurement without
proving where it sent the query and how many operations it performed.

Synthetic tests establish representation and assessment behavior only. They do
not establish resolver selection, DNS wire correctness, DNSSEC, split-DNS/VPN
behavior, permissions, physical networking or production support. #14 and #29
remain open; future UI must preserve these distinctions rather than reducing
responses to a generic DNS success counter.

The [bounded resolver wire boundary](resolver-wire.md) now supplies synthetic,
classic-UDP question/reply validation. It is not yet a sender or execution path.
