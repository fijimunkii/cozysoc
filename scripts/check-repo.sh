#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

required_files=(
  README.md
  LICENSE
  CONTRIBUTING.md
  SECURITY.md
  AGENTS.md
  docs/adr/0001-license.md
  .github/pull_request_template.md
  .github/workflows/ci.yml
  scripts/check-conventional.sh
)

for path in "${required_files[@]}"; do
  if [[ ! -s "$path" ]]; then
    echo "required file is missing or empty: $path" >&2
    exit 1
  fi
done

empty_tree=$(git hash-object -t tree /dev/null)
git diff --check "$empty_tree" HEAD

missing_newline=0
while IFS= read -r path; do
  if [[ -s "$path" && -n $(tail -c 1 "$path") ]]; then
    echo "text file is missing a final newline: $path" >&2
    missing_newline=1
  fi
done < <(git ls-files '*.md' '*.yml' '*.yaml' '*.json' '*.sh' '*.go' 'go.mod' 'go.sum' '.editorconfig' '.gitignore' 'LICENSE')
if (( missing_newline != 0 )); then
  exit 1
fi

while IFS= read -r script; do
  bash -n "$script"
done < <(git ls-files '*.sh')

while IFS= read -r line; do
  target=$(sed -E 's/.*uses:[[:space:]]*([^[:space:]#]+).*/\1/' <<<"$line")

  if [[ $target == ./* ]]; then
    continue
  fi

  if [[ ! $target =~ @[0-9a-f]{40}$ ]]; then
    echo "GitHub Action is not pinned to a full commit SHA: $target" >&2
    exit 1
  fi
done < <(grep -RHE '^[[:space:]]*-[[:space:]]*uses:' .github/workflows || true)

bash scripts/check-conventional.sh \
  'feat: add example capability' \
  'fix(coverage): mark stale sensors' \
  'docs!: revise support contract' \
  'ci(repo): validate pull requests'

if bash scripts/check-conventional.sh 'update things' >/dev/null 2>&1; then
  echo 'conventional subject validator accepted an invalid subject' >&2
  exit 1
fi

echo 'repository checks passed'
