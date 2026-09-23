#!/usr/bin/env bash
set -euo pipefail

image="restic/restic:0.18.0"
target_dir="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"
docker pull "$image"
container="$(docker create "$image")"
trap 'docker rm "$container" >/dev/null 2>&1 || true' EXIT
docker cp "$container:/usr/bin/restic" "$target_dir/restic"
chmod 0755 "$target_dir/restic"
if [[ -n "${GITHUB_PATH:-}" ]]; then
  printf '%s\n' "$target_dir" >> "$GITHUB_PATH"
fi
export PATH="$target_dir:$PATH"
restic version
