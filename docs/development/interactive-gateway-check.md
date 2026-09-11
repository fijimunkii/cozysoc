# Experimental interactive gateway check

Related to #14 and #29. `cozysoc network-quality-check` presents the existing
connection-bound native protocol to a terminal user. It does not add an executor,
change the sender, enable browser authority, persist consent, start a controller,
or remove the explicit experimental macOS startup gate.

## Run deliberately

Start the controller separately, as the normal user in the tested Terminal
context, with `cozysoc serve --state-dir PATH --experimental-gateway-checks`.
Explicitly enroll the network with the existing `network-enroll` flow. Enrollment
still does not enable Device Watch or authorize probes. The controller enforces
its full one-minute startup quiet interval and subsequent admission cooldown.

```text
cozysoc network-quality-check --state-dir PATH TARGET_IPV4
```

Flags precede the single numeric private IPv4 target. There is no `--yes`,
`--approve`, JSON/unattended mode, input-file consent, retry loop, auto-start,
privilege escalation or alternate sender. Help works without a controller.
Unsupported platforms, invalid targets, redirected/piped input or output, root,
and a non-foreground/noncanonical terminal are rejected before controller access.
This command does not expose checks through `dev` or the browser.

The disclosure shows the actual validated target/source/interface/index/prefixes,
profile and expiry, all fixed traffic limits, unverified gateway role, privacy and
local audit retention, and the experimental support boundary. It distinguishes
ICMP request bytes from total wire traffic. It describes only this observation
point, not the whole home, all DNS/internet paths or security posture. Review
challenges, internal scope IDs and approval tickets are not printed.

After the review is written, queued input (including incomplete canonical lines)
is flushed before the prompt. The only affirmative line is `check TARGET_IPV4`
followed by Enter, exactly matching the displayed target. Blank, `yes`, a typo,
a different address or any other line declines. Expiry, EOF, overlong input,
Ctrl-C/SIGTERM, terminal loss and failed output cannot authorize the check. There
is no second prompt or silent retry. This deliberate terminal exchange is not
attestation of an attentive human: same-user automation can operate a PTY.

## Terminal lifetime and deadlines

The Darwin adapter opens its own nonblocking `/dev/tty` descriptor. It requires
stdin/stdout to be the same foreground terminal and verifies canonical/signal
handling. It never changes termios or the shared stdin open-file flags. Fixed-size
input/output and 50 ms select waits check cancellation and the absolute wall-clock
review deadline. There is no goroutine left blocked in a terminal read. Input
remaining after a response is flushed before returning control to the shell;
flush failure prevents approval. The owned descriptor is closed on every exit.
Native testing caught that Darwin's indirect `/dev/tty` does not support the
poll/kqueue readiness path. The adapter uses `select` with a checked fixed-size
descriptor set instead; it never ignores readiness errors or weakens consent.

The typed native client still checks the controller's OS peer identity, validates
the review, keeps one authenticated connection open, and submits only a decision.
The actual ticket stays in the controller. The command cannot change scope,
source/interface or budgets, extend approval expiry, reset cooldown or suppress
the pre-send checks. Cancellation after approval closes that connection; a send
can already be in flight. Such an interrupted/lost result is explicitly unknown,
not a promise of zero traffic or a reason to retry automatically.

## Results and exit status

Plain-text results distinguish execution state from measurement state. They show
a non-authorizing run reference, reviewed numeric provenance, sample times,
send/kernel-acceptance/reply/timeout counters and optional measured RTT. Kernel
acceptance is not proof of wire egress. Missing RTT stays unknown; measured zero
is preserved. Partial samples never turn unsent attempts into timeouts or gain a
loss percentage. Completed zero-reply checks do not declare the internet down.
These are historical samples, not current monitoring or security findings.

Exit 0 means an explicitly declined exchange or an audited, complete sample; it
is **not** an internet-reachability success code. Exit 1 covers invalid/unavailable
input, interrupted exchanges, unconfirmed results, failed/blocked/canceled runs,
and presentation failures. A confirmed partial result is displayed before the
nonzero exit. Errors use curated text, never raw remote/OS/database diagnostics.
The command never retries based on an exit status.

## Validation scope

Portable fixtures test full disclosure before input, exact approval/default
decline, write/flush/read failure, expiry, cancellation, one-call/no-retry behavior,
unknown post-approval results, measured zero/unknown/partial presentation and
rejection of unattended flags before state access.

The existing macOS `native-session` lab now drives the built command through a
real PTY for redirected input/output, blank decline, preloaded approval, overlong
input, EOF, Ctrl-C, real review expiry and final explicit approval. The independent
isolated peer retains its fixed packet/frame limits; its fixture lifetime is
45 seconds (with a separate 50-second hard watchdog) to observe the 30-second
prompt expiry too. Only the final command
may send the three requests. The test then checks real SQLite terminal evidence,
unchanged Device Watch state and absence of observation/coverage records. It also
checks that terminal modes were unchanged while its own controlling-session
owner remains alive. The CLI is not made the PTY session leader, so Darwin cannot
revoke the terminal before that check. The startup cooldown remains real.

The PTY helper owns and reaps exactly its child, canceling on the test's lifetime
pipe EOF. The outer lab monitor also waits for this helper's drain marker. Neither
helper is shipped as a production execution route. This proves tested native
Terminal/PTY behavior, not packaged permission recovery, screen-reader usability,
physical Wi-Fi/NIC egress, actual sleep/resume or arbitrary VPN policy. Those gates
remain open; the experimental switch stays off by default.

Next: bounded retained-result reads and assessment, then appropriate browser/UI
presentation after its separate consent and packaged-permission boundaries.

Primary terminal references: [Apple terminal queue control](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man3/tcflush.3.html)
[Apple select](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/select.2.html),
[Apple poll device limitation](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/poll.2.html),
and [Go Unix system calls](https://pkg.go.dev/golang.org/x/sys/unix).
