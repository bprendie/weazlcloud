#!/usr/bin/env bash
set -euo pipefail

nonce="${WEAZLCLOUD_SHAREDSTORE_RUN_ID:-$(date +%Y%m%d-%H%M%S)-$$}"
volume="weazlcloud-sharedstore-$nonce"
docker volume create "$volume" >/dev/null
cleanup() { docker volume rm "$volume" >/dev/null 2>&1 || true; }
trap cleanup EXIT

make desk-assets
docker run --rm --memory=2g -v "$volume:/data" -v "$PWD:/src:ro" -w /src \
  -e WEAZLCLOUD_SHAREDSTORE_TEST_ROOT=/data/cases \
  -e GOCACHE=/data/gocache -e GOMODCACHE=/data/gomodcache \
  -e GOTMPDIR=/data/tmp -e TMPDIR=/data/tmp \
  golang:1.25-bookworm bash -c '
    set -euo pipefail
    mkdir -p "$WEAZLCLOUD_SHAREDSTORE_TEST_ROOT" "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
    CGO_ENABLED=0 go test ./internal/sharedstore -count=1 -v | tee /data/results.txt
  '
