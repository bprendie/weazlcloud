#!/usr/bin/env bash
set -euo pipefail

status=0
while IFS= read -r file; do
  lines="$(wc -l < "$file")"
  if (( lines >= 300 )); then
    echo "$file: $lines lines (must be below 300)" >&2
    status=1
  fi
done < <(find . -name '*.go' -type f -not -path './.gocache/*' -not -path './.gomodcache/*' | sort)
exit "$status"
