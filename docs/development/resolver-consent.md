# Experimental native resolver consent

Related issues: #14 and #29. A separately started macOS controller may opt in to
one-shot selected-resolver checks:

```sh
cozysoc serve --experimental-resolver-checks
cozysoc resolver-check SELECTION_ID
```

First enroll the network and [save explicit resolver settings](resolver-controller.md).
Ordinary serve/dev/web startup does not expose DNS execution. This flag is separate
from the gateway-check flag, does not start monitoring, and is rejected before
state creation on unsupported platforms. The existing controller-owned singleton
keeps a real one-minute startup quiet interval and one-minute admission interval;
settings updates, previews and new connections cannot reset it.

The check command requires normal-user foreground macOS terminal input and output.
It refuses pipes, redirection, unattended flags, arbitrary endpoints and URLs
before loading controller credentials or dialing. Each review discloses the exact
numeric endpoint, query name/type and expectation, family, destination policy,
source/interface/prefixes, timestamps, all request/receive/time/rate ceilings, and
upstream-forwarding caveat. DNS may leave the enrolled network through recursion
at an enrolled resolver. DNS byte limits exclude lower-layer and server-side traffic.

After disclosure, the command flushes type-ahead and asks for exactly
`check SELECTION_ID`. Anything else declines. There is no `--yes`, raw terminal
mode, automatic retry, execution through web, or persisted approval. The shared
terminal implementation checks deadlines before and after input; output/flush/
input failures prevent approval. Losing a result after approval remains uncertain.

## Protocol and evidence

`network-quality.resolver-check` is a dedicated two-frame native session following
verified OS peer UID, rotating session secret and API-version checks. The first
frame supplies only an opaque saved selection ID. The controller prepares and
holds the original ticket on that connection, then returns a bounded typed review
with an independent random challenge. The second frame supplies only the matching
challenge and explicit boolean approval. Missing/null/duplicate/case-variant or
extra fields are rejected. Challenges cannot approve another connection's review.
The ticket, executable plan and caller-defined callbacks never cross the socket.

Approval consumes the original one-shot ticket. The coordinator commits durable
authorization, reloads configuration/enrollment and route evidence, commits durable
admission, and invokes the existing bound UDP sender. That sender independently
rechecks route/socket binding and enforces one send, bounded receives and deadlines.
There is no TCP, EDNS, referral, alias, host-resolver or alternate-endpoint fallback.
EOF, disconnect, further data or server cancellation cancels the admitted operation;
the connection's reader is closed and joined. Terminal audit confirmation precedes
result publication. An uncertain audit locks the coordinator and publishes no
measurement or inferred permission to retry.

The typed client verifies the controller UID and full review structure and copies
callback input. It validates result provenance, normalized DNS semantics, original
review timing and explicit nanosecond RTT units. Incomplete cleanup can end after
review expiry; a matched response or completed timeout cannot. A matched response
may remain as evidence after a socket-cleanup failure, with failed execution.
Completed execution can report NXDOMAIN, another DNS error or a completed timeout;
it is not a successful-lookup, internet-health or security guarantee. Output keeps
request state, DNS exchange, response code, answer classification and execution
outcome distinct. General errors omit private endpoint/name and raw diagnostics.

## Validation and limits

Packet-free protocol tests use real authenticated Unix sockets and cover strict
request/decision grammar, replay, callback mutation, decline/abandonment, disconnect,
server cancellation, cooldown and uncertain terminal audits. Terminal tests cover
exact approval, default decline, type-ahead ordering, expired/canceled prompts,
output/flush/input failures and unavailable/unattended commands. Projection tests
reject changed policy/budgets, hostile names, stale times and inconsistent results.

The required macOS lab case
`TestMACOSGatewayLab/native-session/resolver-native-session` runs the actual opted-in
controller and resolver CLI through real pseudo-terminals. It saves a synthetic
resolver after explicit feth enrollment, tests redirected input/output, decline,
preloaded input, overlong input, EOF and interrupt before approving once. The
independent peer must observe exactly one 30-byte DNS question and no ICMP; the
actual controller must retain exactly three resolver-run audit phases and a
completed normalized positive response. The existing gateway terminal-expiry gate
and shared-terminal unit checks also remain mandatory. The evidence parser rejects
missing, skipped or failed native cases.

This remains experimental native Terminal support. Virtual feth networking is not
physical Wi-Fi/NIC, VPN, packaged Local Network permission, sleep/resume or whole-home
coverage certification. Resolver retained-history UI, external corroboration and
conservative combined diagnosis remain outstanding; #14 and #29 stay open.
