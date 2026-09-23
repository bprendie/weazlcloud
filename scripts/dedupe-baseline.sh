#!/usr/bin/env bash
set -euo pipefail

id="${WEAZLCLOUD_DEDUPE_RUN_ID:-$(date +%Y%m%d-%H%M%S)-$$}"
volume="weazlcloud-dedupe-baseline-$id"
docker volume create "$volume" >/dev/null
cleanup() { docker volume rm "$volume" >/dev/null 2>&1 || true; }
trap cleanup EXIT

make desk-assets
docker run --rm -v "$volume:/data" -v "$PWD/scripts/dedupe-fixtures.py:/fixture-gen.py:ro" \
  python:3.12-alpine python /fixture-gen.py /data/baseline/fixtures
docker run --rm -v "$volume:/data" --entrypoint /bin/sh restic/restic:0.18.0 \
	  -c 'mkdir -p /data/baseline/bin && cp /usr/bin/restic /data/baseline/bin/restic'
docker run --rm --memory=2g -v "$volume:/data" -v "$PWD:/src:ro" -w /src \
  golang:1.25-bookworm bash -c '
    set -euo pipefail
    export PATH="/data/baseline/bin:$PATH"
    export GOCACHE=/data/baseline/gocache GOMODCACHE=/data/baseline/gomodcache
    export GOTMPDIR=/data/baseline/tmp TMPDIR=/data/baseline/tmp
    export WEAZLCLOUD_BASELINE_ROOT=/data/baseline
    export WEAZLCLOUD_DEDUPE_FIXTURES=/data/baseline/fixtures
    export WEAZLCLOUD_DEDUPE_BASELINE=1
    export WEAZLCLOUD_CHUNK_BASELINE=1
    mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
    go test ./internal/library -run "^TestDedupeBaseline$" -count=1 -v 2>&1 | tee /data/baseline/results.txt
    go test ./internal/sharedstore -run "^TestChunkDedupeBaseline$" -count=1 -v 2>&1 | tee /data/baseline/chunk-results.txt
  '
