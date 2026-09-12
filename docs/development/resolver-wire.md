# Bounded resolver wire boundary

Related work: #14 and #29. `internal/controller/resolverwire` builds one explicitly
selected DNS question and converts a matching reply into the selected-resolver
`DNSReply` evidence contract. It performs no I/O and is not connected to the
controller, CLI, browser, persistence or any execution/consent path.

The query pins a numeric port-53 endpoint, a fully qualified ASCII hostname and
an IN A/AAAA question. It lowercases the hostname and requests recursion. It does
not choose a resolver, append search domains, use host DNS or perform IDNA
conversion. IPv4-mapped, scoped/link-local, multicast and unspecified endpoints
are rejected. Other numeric endpoints still require scope authorization by a
future caller; successful construction is not permission to send traffic.

This initial profile uses classic UDP: 512 bytes, at most 32 resource records
across all sections, and at most eight CNAME links already present in a reply.
There is no EDNS advertisement, TCP fallback, retry, referral query or additional
alias query. Unsolicited OPT records are rejected. Transport family and query
type remain independent. The broader evidence contract can represent transports
and extended response codes that this profile does not implement.

Matching requires the exact numeric peer and port, transaction ID, standard
response opcode, echoed recursion-desired bit, one matching IN question and valid
bounded framing. Question/name comparisons are case insensitive. A complete
question is required even for truncation; a matched TC response retains only its
response code and truncation flag, since the remaining message can be partial.
A future sender must supply kernel-reported peer identity, an unpredictable ID,
source-port entropy, route/source/interface checks, cancellation and budgets.
These checks are not cryptographic authentication or DNSSEC validation.

The maintained BSD-licensed `golang.org/x/net/dns/dnsmessage` package handles DNS
encoding, name expansion and typed resource decoding. A framing pass additionally
requires exact RDATA/message boundaries and backward compression pointers to
previously encountered name-label starts. Names inside unknown record data are
opaque and cannot become compression targets in this strict profile. Unknown
records are bounded and skipped, never treated as answers. The x/net v0.59.0
module requires the accompanying x/sys v0.48.0 update.

A positive classification requires the selected type at the question name or
through an unambiguous, bounded in-message CNAME chain. Cycles, conflicting
aliases, an alias mixed with an address at the same owner, unrelated/wrong-type
answers and unresolved aliases remain unclassified. Relevant authority SOA
supports NODATA; authority NS without SOA is a referral. Empty NOERROR without
authority NS is NODATA under RFC 2308. Error response codes remain distinct and
carry no answer classification. Additional-section addresses cannot satisfy the
question. These are response-content classifications, not assertions that DNS
data is correct, a resolver is secure, or the internet is reachable.

Only normalized evidence or a fixed error leaves matching. No queried name,
answer address, raw packet or detailed parser error is returned. Query objects
and their byte copies contain the selected name and must not be logged.

Synthetic unit and fuzz tests cover matching, classification, compression,
truncated messages, malformed lengths and resource limits. They establish no
physical-network, resolver-selection, split-DNS/VPN or production execution
support. A real sender still needs explicit endpoint/name disclosure and immutable
configuration, admitted scope, one-shot consent, durable audit, retention,
cooldown/concurrency control and bounded request/deadline behavior. #14 and #29
remain open.

The [selected-resolver review plan](resolver-review.md) now pins explicit
endpoint/query/scope disclosure and proposed one-shot budgets. It grants no
consent and is not yet connected to a sender.
