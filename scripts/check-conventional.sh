#!/usr/bin/env bash
set -euo pipefail

if (( $# == 0 )); then
  echo "usage: bash scripts/check-conventional.sh <subject> [<subject> ...]" >&2
  exit 2
fi

pattern='^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9][a-z0-9._/-]*\))?(!)?: .+[^[:space:]]$'
failed=0

for value in "$@"; do
  subject=${value%%$'\n'*}

  if [[ $subject =~ $pattern ]]; then
    continue
  fi

  echo "invalid conventional subject: $subject" >&2
  echo "expected: <type>(optional-scope): short imperative summary" >&2
  echo "allowed types: feat fix docs style refactor perf test build ci chore revert" >&2
  failed=1
done

exit "$failed"