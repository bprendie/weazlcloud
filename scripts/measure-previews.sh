#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

echo "Preview decode baseline: synthetic 1200x800 PNG, 320px thumbnails"
echo "The benchmark measures bounded renderer work, not network or browser paint time."
go test ./internal/library -run '^$' -bench 'BenchmarkThumbnailDecode(100|500)$' -benchmem -count=1
