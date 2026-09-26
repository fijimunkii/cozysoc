# Experimental local browser gateway check

Related to #14 and #29. The authenticated local browser can request one bounded
ICMP check of a user-selected numeric private IPv4 address on the enrolled
network. It uses the existing controller-owned native gateway consent protocol
and sender. The controller still requires macOS startup with
`--experimental-gateway-checks`; ordinary `serve`, `dev`, enrollment and Device
Watch enablement do not turn checks on. HTTPS and resolver checks remain on
their separate native paths.

The live Overview presents a target field and **Review one-shot check**. A
successful review performs the read-only `network-quality.gateway-plan` call,
including fresh interface and route/source inspection, and returns the target,
source, interface/index, enrolled prefixes, fixed ICMP request limits and a
30-second expiry. No packet is sent by review. The selected target's gateway
role is unverified. The page explains destination/network visibility and local
audit retention before showing a separate **Approve one check** action. A
synthetic demo cannot review or run a check. The page disables approval after
expiry or focus loss. Browser navigation, refresh or error does not approve or
retry a check.

`POST /api/network-quality/gateway/review` accepts only `{ "target": "..." }`;
`POST /api/network-quality/gateway/run` accepts only a one-use random review ID
and an explicit boolean decision. Both require the web session, exact local
Origin and CSRF header, strict JSON and no query string. The Go web process
holds at most one short-lived review in memory; a decision consumes it before
contacting the controller. Decline contacts no execution session. Nothing is
stored as a reusable browser grant. Another check needs another review and
approval.

On approval, the web process opens a fresh authenticated native consent
connection. It approves the controller's one-shot native review only if target,
source, complete enrolled binding, budget and time window match the displayed
plan. The controller then performs its own pre-send checks, cooldown, one-run
limit, audit and bounded send. The controller's challenge and execution ticket
never enter browser JSON. No arbitrary controller method, shell command,
destination hostname, packet body or sender option is accepted through these
routes. The web process does not broaden loopback access to remote clients.

An acknowledged result separates execution outcome from the available
measurement. Missing or partial measurements remain unknown or partial; a
zero-reply sample is not an internet-down verdict. Once approval is submitted,
a lost or malformed result is **outcome unknown** because traffic may already
have been sent. The UI directs the user to the read-only saved history and
never retries automatically. Historical reads never repeat traffic.

Go tests cover the web mutation boundary, target validation, controller-plan
projection, one-use decision, changed native review, decline, expiry and lost
post-approval outcome. Component tests cover separate disclosure/approval,
stale review and uncertainty. The rendered Chromium journey checks the real
page's request sequence, narrow/200% reflow and automated accessibility rules
against fixture responses. The existing isolated macOS native lab owns actual
packet, audit and coordinator evidence. A packaged app permission/recovery
path, physical-interface egress, sleep/resume, VPN behavior and human
screen-reader/usability validation remain release gates; this experimental
browser flow does not claim those gates passed.
