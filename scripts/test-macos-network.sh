#!/usr/bin/env bash
# Explicit disposable macOS VM lab. Product code runs as the normal user in
# Terminal, not under sudo. Scoped test-certificate trust is revoked on teardown.
set -euo pipefail
[[ $(uname -s) == Darwin && ${COZYSOC_MACOS_LAB:-} == 1 ]]
[[ $(id -u) != 0 ]]
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_ENVIRONMENT:-} == github-hosted ]]
cd "$(git rev-parse --show-toplevel)"
umask 077
work=$(mktemp -d "$HOME/.czlab.XXXXXX")
left=0; right=0
printf 'github-hosted\n' > "$work/disposable-trust-allowed"
cleanup() {
  status=$?
  trap - EXIT
  # Revoke a delayed Terminal launch and ask the child-owning monitor to stop.
  touch "$work/abort"
  rm -f "$work/run.command"
  if [[ -f "$work/started" && ! -f "$work/exit-code" ]]; then
    # Allow the owned lab child and controller supervisor to finish their joins.
    for ((i=0; i<220; i++)); do
      [[ -f "$work/exit-code" ]] && break
      sleep 0.1
    done
    if [[ ! -f "$work/exit-code" ]]; then
      echo 'lab monitor did not confirm shutdown' >&2
      status=1
    fi
  fi
  if [[ -f "$work/https-trust.pem" && ! -f "$work/https-trust-revoked" ]]; then
    sudo -n /usr/bin/python3 "$work/trust-cleanup.py" || status=1
  fi
  if (( right )); then sudo -n /sbin/ifconfig feth43 destroy || status=1; fi
  if (( left )); then sudo -n /sbin/ifconfig feth42 destroy || status=1; fi
  if (( left )) && /sbin/ifconfig feth42 >/dev/null 2>&1; then status=1; fi
  if (( right )) && /sbin/ifconfig feth43 >/dev/null 2>&1; then status=1; fi
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
/usr/bin/clang -std=c11 -O2 -Wall -Wextra -Werror tests/macoslab/testdata/peer.c -lpcap -o "$work/peer"
"$work/peer" check
# Test-only TCP stack, never linked into the product. Version changes need review.
[[ $(pkg-config --modversion slirp) == 4.9.4 ]]
/usr/bin/clang -std=c11 -O2 -Wall -Wextra -Werror $(pkg-config --cflags slirp) tests/macoslab/testdata/https-peer.c -lpcap $(pkg-config --libs slirp) -o "$work/https-peer"
"$work/https-peer" check
if /sbin/ifconfig feth42 >/dev/null 2>&1 || /sbin/ifconfig feth43 >/dev/null 2>&1; then
  echo 'refusing to replace existing lab interfaces' >&2
  exit 1
fi
# Complete compilation before changing the VM's isolated network.
go test -c -o "$work/lab.test" ./tests/macoslab
go build -o "$work/cozysoc" ./cmd/cozysoc
cp scripts/macos-lab-runner.py "$work/run.py"
cp scripts/macos-lab-trust.py "$work/trust-cleanup.py"
cp tests/macoslab/testdata/cli_driver.py "$work/cli-driver.py"
printf '#!/bin/bash\nexec %q %q\n' "$(command -v python3)" "$work/run.py" > "$work/run.command"
chmod 700 "$work/run.command"
sudo -n /sbin/ifconfig feth42 create
left=1
sudo -n /sbin/ifconfig feth43 create
right=1
sudo -n /sbin/ifconfig feth42 peer feth43
sudo -n /sbin/ifconfig feth42 inet 192.168.250.2/24 up
sudo -n /sbin/ifconfig feth43 up
# LaunchServices/Terminal provides the actual non-root CLI execution context.
# This is not evidence for a packaged app's Local Network permission UX.
/usr/bin/open -a Terminal "$work/run.command"
for ((i=0; i<240; i++)); do
  [[ -f "$work/exit-code" ]] && break
  sleep 1
done
if [[ -f "$work/error" ]]; then cat "$work/error"; fi
if [[ -f "$work/output" ]]; then
  go tool test2json -t -p github.com/fijimunkii/cozysoc/tests/macoslab < "$work/output" | tee "$work/results.jsonl"
fi
if [[ ! -f "$work/exit-code" ]]; then
  echo 'Terminal lab did not return a confirmed exit status' >&2
  exit 1
fi
[[ $(cat "$work/exit-code") == 0 ]]
python3 scripts/check-macos-lab.py "$work/results.jsonl"
