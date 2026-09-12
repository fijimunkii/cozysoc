# Native macOS isolated network lab

Related to #14 and #29. This is native runtime and virtual-network evidence,
not hardware certification or proof of packaged permission behavior.

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
eight named cases to pass, rejects skips/failures/unexpected packages, and requires
a successful package result. An empty or partially executed suite cannot pass.
Portable regression tests exercise this checker and the child-owning launcher.
The six packet cases now pass through the real coordinator and adapter as well:
review creates no audit, admission precedes the sender, terminal sample counts
match the result, and consumed-ticket replay is refused. The lab auditor is
in-memory; separate real-SQLite tests prove durable measurement round-trips and
write-failure behavior. Only the construction clock is aged by a minute to avoid
a cooldown wait per fixture; all review, sample and audit times use the real clock.

```text
Normal-user test process launched through Terminal
  -> actual one-shot gatewayrun coordinator + ICMP adapter
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
| Shutdown | Close the coordinator after the first observed request; require terminal audit drain and no second request. |
| Native session | Actual opted-in controller and interactive CLI/PTY, full startup quiet minute, decline/expiry/interruption/redirection, explicit approval, real replies and SQLite terminal evidence. |
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

Measurements now pass through the audited one-shot coordinator in the lab.
The [controller-owned lifecycle](gateway-controller-lifecycle.md) remains dormant
by default. The new [experimental native consent case](gateway-consent-session.md)
exercises the actual controller, authenticated exchange and real SQLite audit.
The [interactive command](interactive-gateway-check.md) is now exercised through
real pseudo-terminals within that same session, including decline, type-ahead,
expiry, interruption, redirection and one explicit approved sample. There is
still no browser execution control. Validate the packaged product's
permission-denied/recovery behavior and the remaining hardware scenarios without
turning fixture success into a broad support claim. Enrollment still does not
enable Device Watch or grant one-shot probe consent. Neither #14 nor #29 is closed
by this lab.

## Real controller process in the native consent case

The `native-session` case waits the real one-minute startup quiet interval rather
than aging the construction clock used by the smaller coordinator fixtures. A
private controller monitor owns the actual child; stdin EOF revokes its lifetime
even if the Go test dies. The outer monitor waits for child-drain confirmation,
without signaling a PID read from a file. The dedicated lab test timeout is 210
seconds and its monitor ceiling is 220 seconds; these changes do not alter any
production probe or consent budget. See the native consent document for claims
and remaining packaged/hardware limits.

## Restricted HTTPS TCP peer

The HTTPS lab adds a test-only [libslirp](https://gitlab.freedesktop.org/slirp/libslirp)
peer, baseline version 4.9.4 (BSD-3-Clause), supplied by Homebrew with pkgconf.
It is compiled only into the disposable lab helper and is not linked into or
installed with Cozy SOC. CI rejects a changed version until the baseline is
reviewed. This is test infrastructure, not a new product security engine or
runtime dependency. libslirp owns TCP state, checksums and retransmissions; the
fixture does not implement a TCP stack.

The helper opens BPF only on the existing isolated `feth43`, then drops all root
and supplementary-group authority before creating the TCP stack or processing
frames. Its only service is `192.168.250.1:443`, forwarded by libslirp to the
invoking user's mode-0600 `https.sock` in the private lab directory. Restricted
networking is enabled; IPv6, DNS, DHCP, host-loopback access and emulation are
disabled. No external address, command execution or host forwarding is configured.
The virtual `.254` address is used only for libslirp neighbor resolution.

Ingress admits only fixed-source ARP and unfragmented IPv4 TCP to the fixed
service. Frame sizes/counts, poll sockets and lifetime are bounded. EOF on the
owned input pipe stops the helper; the parent joins it before teardown. The
existing harness checks fixture conflicts before changing isolated interfaces.

`https-native-tls-rejection` exercises native route collection, the production
bound TCP candidate and a real TLS handshake through this peer. An ephemeral
self-signed certificate is generated in memory by the owned Unix-socket TLS
server. Production system trust must reject it, with TLS-stage error, no HTTP
request accepted, no status and no response latency. No trust store is modified.
This establishes native TCP/TLS rejection through the isolated peer, not trusted
HTTPS success, real physical NICs or complete controller/PTY consent support.
The following trusted-response and integrated consent cases provide additional
evidence for the default-off experimental macOS execution flag.

## Trusted HTTPS response and trust cleanup

The required `https-native-trusted-response` case adds a unique, short-lived
self-signed test identity to the disposable runner's system keychain, with trust
restricted to SSL for that exact hostname. The private key remains in memory.
The production candidate uses normal system trust: no custom root pool or
verification bypass is supplied. The owned TLS server validates the HEAD target,
Host and fixed user agent, rejects cookies/authorization headers, and returns 204.
The candidate must retain request acceptance, status and measured response timing.

This trust-changing suite now requires both `GITHUB_ACTIONS=true` and
`RUNNER_ENVIRONMENT=github-hosted` before creating its private work directory. It
must not be run on a developer workstation or a persistent/self-hosted runner.
The shell creates a private marker for the Terminal-launched process only after
those checks. Existing native code remains unprivileged; only narrow certificate
installation/removal and isolated-interface setup use sudo.

Before adding trust, the test records the public certificate and its SHA-256 hash
in mode-0600 files. Cleanup exports current administrative trust, replaces the
fixture's exact SSL grant with an explicit deny policy, and verifies every other
entry is unchanged. It then deletes exactly that certificate by hash and verifies
that normal system trust rejects the identity. Each security-tool child has a
ten-second bound and is joined by its privileged supervisor.

The deny record remains until the hosted VM is disposed. This is verified trust
revocation and certificate deletion, not deletion of all trust metadata. Removing
the last administrative trust entry hangs waiting for authorization on the tested
runners; changing the authorization rule is rejected. The harness changes no
authorization rules and does not weaken production verification to work around it.
Only confirmed cleanup writes the completion marker. If the test is interrupted,
the outer harness attempts the same revocation from the journal and fails on an
unconfirmed operation. No unrelated certificates or trust settings are replaced.
A forced VM termination relies on disposal of the hosted runner.

The required `native-session/https-native-session` starts the actual opted-in
controller and drives `https-check` through real PTYs. It requires full startup
quiet time, disclosure, redirected-I/O rejection, default/type-ahead decline,
overlong input, EOF, Ctrl-C, original review expiry, an approved HEAD/204 response,
response timing and cooldown. After joining the controller it checks exactly
three persisted HTTPS audit phases and no monitoring observations/coverage.
The controller session has a 170-second deadline and a 180-second owner ceiling;
the outer Terminal launcher waits at most 240 seconds. These bounds allow the
real HTTPS review expiry in addition to the existing gateway/resolver cases.
They do not shorten or bypass product deadlines or consent.

This is isolated controller/TCP/TLS/PTY evidence, not physical NIC, packaged
permission recovery, VPN or sleep/resume certification. It enables only the
explicit macOS `--experimental-https-checks` opt-in.
