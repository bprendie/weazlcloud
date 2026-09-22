#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${BROWSER_BIN:-}" ]]; then
  browser="$BROWSER_BIN"
else
  browser=""
  for candidate in chromium chromium-browser google-chrome; do
    if command -v "$candidate" >/dev/null 2>&1; then
      browser="$candidate"
      break
    fi
  done
fi
if [[ -z "$browser" ]]; then
  echo "browser smoke requires Chromium or Google Chrome" >&2
  exit 1
fi

port="${WEAZLCLOUD_BROWSER_PORT:-17272}"
data_dir="$(mktemp -d)"
log_file="$(mktemp)"
cleanup() {
  if [[ -n "${pid:-}" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -f "$log_file"
  rm -rf "$data_dir"
}
trap cleanup EXIT

WEAZLCLOUD_DATA="$data_dir" \
WEAZLCLOUD_DESK_ADDR="127.0.0.1:$port" \
WEAZLCLOUD_SHARE_ADDR="127.0.0.1:$((port + 1))" \
WEAZLCLOUD_DRIVE_ADDR="127.0.0.1:$((port + 2))" \
go run ./cmd/weazlcloud >"$log_file" 2>&1 &
pid=$!
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/live" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
curl -fsS "http://127.0.0.1:$port/ready" >/dev/null
dom="$($browser --headless --no-sandbox --disable-gpu --dump-dom "http://127.0.0.1:$port/" 2>/dev/null)"
grep -Fq "WeazlCloud / the household node" <<<"$dom"
grep -Fq "library" <<<"$dom"
