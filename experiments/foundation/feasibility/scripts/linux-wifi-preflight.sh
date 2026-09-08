#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <dedicated-wifi-interface>" >&2
  exit 2
fi

iface=$1
if [[ ! -e "/sys/class/net/$iface" ]]; then
  echo "interface does not exist: $iface" >&2
  exit 1
fi

if ! command -v iw >/dev/null 2>&1; then
  echo "iw is required for this preflight" >&2
  exit 1
fi

primary=$(ip route show default 2>/dev/null | awk '/default/ {for (i=1;i<=NF;i++) if ($i=="dev") {print $(i+1); exit}}')
if [[ -n ${primary:-} && $iface == "$primary" ]]; then
  echo "refusing to use $iface: it is the current IPv4 default-route interface" >&2
  echo "use a dedicated USB Wi-Fi adapter so the experiment cannot disrupt management connectivity" >&2
  exit 1
fi

primary6=$(ip -6 route show default 2>/dev/null | awk '/default/ {for (i=1;i<=NF;i++) if ($i=="dev") {print $(i+1); exit}}')
if [[ -n ${primary6:-} && $iface == "$primary6" ]]; then
  echo "refusing to use $iface: it is the current IPv6 default-route interface" >&2
  exit 1
fi

phy=""
if [[ -e "/sys/class/net/$iface/phy80211/name" ]]; then
  phy=$(cat "/sys/class/net/$iface/phy80211/name")
fi

printf '# Cozy SOC USB Wi-Fi preflight\n\n'
printf 'captured_at_utc: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
printf 'kernel: %s\n' "$(uname -srmo)"
printf 'interface: %s\n' "$iface"
printf 'phy: %s\n' "${phy:-unknown}"
printf 'default_route_interface: %s\n' "${primary:-none}"
printf 'default_ipv6_route_interface: %s\n' "${primary6:-none}"

if command -v ethtool >/dev/null 2>&1; then
  printf '\n## Driver / firmware\n'
  ethtool -i "$iface" || true
fi

printf '\n## Interface (addresses redacted)\n'
ip -details link show "$iface" 2>/dev/null \
  | sed -E 's/(link\/(ether|loopback) )[0-9a-fA-F:]+/\1[redacted]/g' || true

printf '\n## Wireless capabilities\n'
if [[ -n $phy ]]; then
  if iw phy "$phy" info | awk '
    /Supported interface modes:/ {inside=1; next}
    inside && /^[[:space:]]*Band / {inside=0}
    inside && /\* monitor/ {found=1}
    END {exit found ? 0 : 1}
  '; then
    echo 'monitor_mode: supported-by-driver-report'
  else
    echo 'monitor_mode: not-reported'
  fi
else
  echo 'monitor_mode: unknown-no-phy80211'
fi

printf '\n## USB devices (no serial query)\n'
if command -v lsusb >/dev/null 2>&1; then
  lsusb || true
else
  echo 'lsusb unavailable'
fi

printf '\npreflight_result: PASS-dedicated-interface-not-on-default-route\n'
printf 'note: this is capability evidence only; Kismet capture, channel behavior, unplug/replug, and cleanup still require real tests.\n'
