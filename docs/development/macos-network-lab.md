# Native macOS isolated network lab

Related to #14 and #29. This is native runtime and virtual-network evidence,
not a live product control or a hardware support certification.

## What runs

Normal PR/main CI executes native Go tests and race-enabled gateway route,
ICMP, run-coordinator and local-API tests on standard `macos-15` and `macos-26`
Apple Silicon runners. It then explicitly runs `scripts/test-macos-network.sh`.
The established `Repository checks` job requires the entire native matrix to
succeed; an `always()` guard and explicit result check prevent a failed/skipped
native dependency from silently skipping the aggregate. Branch-protection
settings are not changed by this workflow.

Normal `go test ./...` remains packet-free unless the isolated lab is explicitly
selected. In the live job, `check-macos-lab.py` requires the parent test and all
six named cases to pass, rejects skips/failures/unexpected packages, and requires
a successful package result. An empty or partially executed suite cannot pass.
Portable regression tests exercise this checker and the child-owning launcher.

```text
Normal-user test process launched through Terminal
  -> actual Cozy SOC gatewayroute + gatewayicmp implementations
  -> Darwin route/data sockets and source/interface checks
  -> feth42 <-> feth43 isolated Ethernet pair
  -> bounded ARP/ICMP fixture peer
  -> actual Darwin receive controls and strict reply matching
```

The fixed source is `192.168.250.2/24` on `feth42`. The peer represents
`192.168.250.1` using only the paired `feth43` capture descriptor. The peer address
is never assigned to the host, so this is not a loopback/self-target substitute.
The pair has no bridge to a physical or provider network. No default gateway,
DNS resolver, external endpoint or arbitrary discovered host is probed.

## Cases and assertions

| Case | Evidence required |
| --- | --- |
| Route | Real source/index agree; wrong interface index and self-target fail. |
| Reply | Peer sees three 40-byte echoes; sender matches three replies and reports measured RTT. |
| Silent | Peer sees three requests but sends no ICMP replies; three timeouts with unknown RTT. |
| Wrong nonce | Peer sends checksum-valid but mismatched replies; none becomes a matched reply. |
| Cancel | Cancel after the peer observes the first request; incomplete result and no second request. |
| Source loss | Remove the exact source while retaining another address in the same prefix; no second request or complete result. |

The observer checks sequence, count, ICMP size and capture-time spacing
independently of the sender's returned counters. Its burst assertion uses a
950 ms capture-time threshold to allow scheduling jitter; the sender still
requires at least one second between attempts. This is not precision hardware
timing certification. The cancel/source-loss cases observe a further 1.1 seconds
after the sender returns to catch a queued second request.

The responder handles only the fixed pair and reviewed packet forms. It bounds
runtime, captured frames, ARP responses and ICMP replies. It retains no packet
capture and outputs only fixture state/count/timing records. The C helper links
the runner's system libpcap; there is no new product Go/npm dependency.

## Privileges, launch context and cleanup

The script refuses root execution, an existing `feth42`/`feth43`, or an overlapping
existing IPv4 subnet. It compiles before changing the VM network. `sudo` is used
only for the fixture interface operations and opening/configuring the responder's
BPF descriptor. The responder irreversibly drops its UID/GID and supplementary
groups before handling packets; the tests assert the resulting UID is their own
non-root UID. The actual Cozy SOC sender never runs under `sudo`.

The live process is launched with `open -a Terminal` and a private `.command`
file. A child-owning Python monitor enforces timeout/abort, reaps the exact child,
and atomically records its exit status. The shell revokes delayed launch, signals
an abort marker, and destroys only interfaces it successfully created. It does
not use broad `pkill`, trust a PID file as process ownership, or terminate an
unrelated Terminal session. A finished Terminal window may remain until the
disposable runner is torn down. Do not run this harness on an everyday workstation.

No privacy database, Local Network permission preference, firewall, SIP,
physical-interface, DNS or default-route setting is changed. The harness has no
SSH server or launchd service. It does not automate permission-dialog acceptance.

**Launch context is part of the evidence.** During feasibility, the direct runner,
a non-root launchd job and a normal-user private SSH session failed the send path
with `EHOSTUNREACH`, even though route and socket setup succeeded. Minimal C and Go
senders reproduced it, while Apple's ping could reach the same isolated peer.
The unchanged Cozy SOC sender passed all six cases when launched through Terminal.
This establishes a context-dependent runtime difference; the errno alone does
not identify the specific OS policy or prove that the network is unreachable.
Do not translate this result into a verified packaged-app/LaunchAgent permission
flow, or change production privileges to imitate a passing test environment.

The first full live pass was on September 11, 2026, `macos-15` arm64,
macOS 15.7.9 (24G830), runner image 20260907.0337.1:
[feasibility run](https://github.com/fijimunkii/cozysoc/actions/runs/34609361161).
The permanent exact-head CI matrix is the evidence for subsequent revisions and
runner versions. Log `sw_vers` and `uname -m`; do not treat a floating image label
as an immutable OS/kernel version.

`TMPDIR=/private/tmp` gives native tests short, canonical temporary paths. The
runner's default deep temporary path exceeded Darwin Unix-socket path limits in
some tests. Using `/tmp` instead exposed a symlink/canonical-root mismatch in a
static-file fixture. Neither failure was fixed by weakening product path checks.

## Native route-decoder regression

The first real route read exposed a fixture gap: Darwin's compressed radix
netmask contains mask/header bytes where an ordinary sockaddr would carry an
address family and port. A kernel `/24` mask used the bounded byte sequence
`07 ff ff ff ff ff ff`. Decode its address bits using the separately validated
IPv4 destination, rather than rejecting those bytes as an invalid sockaddr
family. Length, contiguous-mask, destination, source and interface validation
remain in force. Portable regressions cover the observed form and malformed
noncontiguous masks; the live route test exercises the actual kernel response.

## Still separate release gates

This lab proves selected real kernel/socket behavior against a controlled virtual
peer, including real correlated replies, timeout semantics, cancellation and
exact-source removal. It does not prove Wi-Fi/physical NIC egress, real hardware
sleep/resume, reboot or service registration, arbitrary VPN/Network Extension
policies, interface-index recycling, sustained overload, or GUI accessibility.
Native local-API tests are not a claim that the full installed service/UI consent
journey has been exercised on macOS. Existing process E2E remains a separate job.

Before exposing live controls, preserve measurements through the audited one-shot
coordinator, install its single controller-owned lifecycle, and exercise the
actual authenticated consent/result path. Validate the packaged product's
permission-denied/recovery behavior and the remaining hardware scenarios without
turning fixture success into a broad support claim. Enrollment still does not
enable Device Watch or grant one-shot probe consent. Neither #14 nor #29 is closed
by this lab.
