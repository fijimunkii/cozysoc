#!/usr/bin/env bash
set -euo pipefail

label='com.cozysoc.feasibility'
domain="gui/$UID"
plist="$HOME/Library/LaunchAgents/$label.plist"

xml_escape() {
  local value=$1
  value=${value//&/&amp;}
  value=${value//</&lt;}
  value=${value//>/&gt;}
  printf '%s' "$value"
}

usage() {
  cat >&2 <<'USAGE'
usage:
  macos-launchd-lifetime.sh install <absolute-probe-binary> <absolute-heartbeat-file>
  macos-launchd-lifetime.sh status
  macos-launchd-lifetime.sh uninstall

This validates launchd-owned lifetime only. It does NOT validate final app-bundled SMAppService registration.
USAGE
  exit 2
}

[[ $(uname -s) == Darwin ]] || { echo 'macOS only' >&2; exit 1; }
[[ $# -ge 1 ]] || usage

case "$1" in
  install)
    [[ $# -eq 3 ]] || usage
    binary=$2
    heartbeat=$3
    [[ $binary == /* && -x $binary ]] || { echo 'probe binary must be an absolute executable path' >&2; exit 1; }
    [[ $heartbeat == /* ]] || { echo 'heartbeat path must be absolute' >&2; exit 1; }
    [[ $binary != *$'\n'* && $heartbeat != *$'\n'* ]] || { echo 'paths must not contain newlines' >&2; exit 1; }
    mkdir -p "$(dirname "$heartbeat")" "$HOME/Library/LaunchAgents"
    binary_xml=$(xml_escape "$binary")
    heartbeat_xml=$(xml_escape "$heartbeat")
    cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>$binary_xml</string>
    <string>service</string>
    <string>--heartbeat</string><string>$heartbeat_xml</string>
    <string>--interval</string><string>1s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/$label.stdout.log</string>
  <key>StandardErrorPath</key><string>/tmp/$label.stderr.log</string>
</dict>
</plist>
PLIST
    chmod 600 "$plist"
    launchctl bootout "$domain/$label" >/dev/null 2>&1 || true
    launchctl bootstrap "$domain" "$plist"
    launchctl kickstart -k "$domain/$label"
    echo "installed $label"
    echo "close the terminal/UI, sleep/resume if testing that case, then run: $0 status"
    ;;
  status)
    [[ $# -eq 1 ]] || usage
    launchctl print "$domain/$label"
    ;;
  uninstall)
    [[ $# -eq 1 ]] || usage
    launchctl bootout "$domain/$label" >/dev/null 2>&1 || true
    rm -f "$plist"
    echo "removed $label"
    ;;
  *) usage ;;
esac
