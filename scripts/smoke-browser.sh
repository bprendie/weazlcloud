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
storage_backend="${WEAZLCLOUD_SMOKE_STORAGE_BACKEND:-restic}"
data_dir="$(mktemp -d)"
log_file="$(mktemp)"
binary="$(mktemp)"
cleanup() {
  if [[ -n "${pid:-}" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -f "$log_file"
  rm -f "$binary"
  rm -rf "$data_dir"
}
trap cleanup EXIT

CGO_ENABLED=0 go build -o "$binary" ./cmd/weazlcloud
WEAZLCLOUD_DATA="$data_dir" \
WEAZLCLOUD_DESK_ADDR="127.0.0.1:$port" \
WEAZLCLOUD_SHARE_ADDR="127.0.0.1:$((port + 1))" \
WEAZLCLOUD_DRIVE_ADDR="127.0.0.1:$((port + 2))" \
WEAZLCLOUD_STORAGE_BACKEND="$storage_backend" \
"$binary" >"$log_file" 2>&1 &
pid=$!
live=0
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/live" >/dev/null 2>&1; then
    live=1
    break
  fi
  sleep 0.25
done
if [[ "$live" != 1 ]]; then
  cat "$log_file" >&2
  echo "node did not become live" >&2
  exit 1
fi
curl -fsS "http://127.0.0.1:$port/ready" >/dev/null
jar="$data_dir/browser-cookies.txt"
curl -fsS -c "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"username":"browser","password":"browser-pass","vault_passphrase":"browser-vault","confirm":"browser-vault"}' \
  "http://127.0.0.1:$port/api/bootstrap" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"passphrase":"browser-vault"}' "http://127.0.0.1:$port/api/unlock" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' "http://127.0.0.1:$port/api/me" | grep -Fq 'browser'
dom="$($browser --headless --no-sandbox --disable-gpu --dump-dom "http://127.0.0.1:$port/" 2>/dev/null)"
grep -Fq "WeazlCloud / the household node" <<<"$dom"
grep -Fq "library" <<<"$dom"
