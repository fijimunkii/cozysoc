#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 DESTINATION (an unused directory)" >&2
  exit 2
fi

repo_root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
destination_name=$(basename "$1")
if [[ $destination_name == . || $destination_name == .. || $destination_name == / ]]; then
  echo "destination must name a new directory" >&2
  exit 2
fi
destination_parent=$(cd "$(dirname "$1")" && pwd -P)
destination="$destination_parent/$destination_name"
if [[ -e $destination || -L $destination ]]; then
  echo "destination already exists: $destination" >&2
  exit 2
fi

cd "$repo_root"
stage=$(mktemp -d "$destination.tmp.XXXXXX")
trap 'rm -rf "$stage"' EXIT

npm --prefix ui ci --ignore-scripts --no-audit --no-fund
npm --prefix ui run build
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o "$stage/cozysoc" ./cmd/cozysoc
mkdir -p "$stage/ui/dist"
cp -R ui/dist/. "$stage/ui/dist/"

source_commit=$(git rev-parse HEAD)
source_state=clean
if [[ -n $(git status --porcelain) ]]; then
  source_state=modified
fi
cat > "$stage/BUILD-INFO.txt" <<EOF
Cozy SOC unsigned developer bundle
Source commit: $source_commit
Source tree: $source_state
Platform: $(go env GOOS)/$(go env GOARCH)
Release support: none
EOF

if [[ -e $destination || -L $destination ]]; then
  echo "destination appeared during build: $destination" >&2
  exit 2
fi
mv "$stage" "$destination"
trap - EXIT
echo "Built unsigned developer bundle: $destination"
