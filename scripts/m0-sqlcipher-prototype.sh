#!/usr/bin/env bash
set -euo pipefail

# M0 only: exercise encrypted Photos metadata in a disposable container. This
# never opens a WeazlCloud vault and never touches production data.
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/weazl-m0-sqlcipher.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

docker run --rm --cpus=2 --memory=4g \
  -v "$work_dir:/work" \
  -v "$repo_root/scripts/m0-sqlcipher-inner.sh:/usr/local/bin/m0-inner:ro" \
  alpine:3.21 sh /usr/local/bin/m0-inner
