#!/usr/bin/env bash
set -euo pipefail

base_port="${WEAZLCLOUD_RECOVERY_PORT:-18272}"
root="$(mktemp -d)"
data_dir="$root/data"
restored_dir="$root/restored"
jar="$root/cookies.txt"
log_file="$root/node.log"
payload="$root/payload.bin"
binary="$root/weazlcloud"
pid=""

cleanup() {
  if [[ -n "$pid" ]]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$root"
}
trap cleanup EXIT

printf 'weazl recovery smoke payload\n' >"$payload"
expected="$(sha256sum "$payload" | awk '{print $1}')"
CGO_ENABLED=0 go build -o "$binary" ./cmd/weazlcloud

start_node() {
  local node_data="$1"
  local node_log="$2"
  WEAZLCLOUD_DATA="$node_data" \
  WEAZLCLOUD_DESK_ADDR="127.0.0.1:$base_port" \
  WEAZLCLOUD_SHARE_ADDR="127.0.0.1:$((base_port + 1))" \
  WEAZLCLOUD_DRIVE_ADDR="127.0.0.1:$((base_port + 2))" \
  "$binary" >"$node_log" 2>&1 &
  pid=$!
  for _ in $(seq 1 80); do
    if curl -fsS "http://127.0.0.1:$base_port/live" >/dev/null 2>&1; then
      return
    fi
    sleep 0.25
  done
  cat "$node_log" >&2
  echo "node did not become live" >&2
  exit 1
}

stop_node() {
  kill "$pid"
  wait "$pid" 2>/dev/null || true
  pid=""
}

start_node "$data_dir" "$log_file"
curl -fsS "http://127.0.0.1:$base_port/ready" >/dev/null

bootstrap="$(curl -fsS -c "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"username":"recovery","password":"recovery-pass","vault_passphrase":"recovery-vault","confirm":"recovery-vault"}' \
  "http://127.0.0.1:$base_port/api/bootstrap")"
user_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$bootstrap")"
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"passphrase":"recovery-vault"}' "http://127.0.0.1:$base_port/api/unlock" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' --upload-file "$payload" \
  "http://127.0.0.1:$base_port/api/library?path=payload.bin" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$base_port/api/library?path=payload.bin" -o "$root/live.bin"
actual="$(sha256sum "$root/live.bin" | awk '{print $1}')"
[[ "$actual" == "$expected" ]] || { echo "live hash mismatch" >&2; exit 1; }

mkdir -p "$data_dir/users/$user_id/.weazl-archives"
touch "$data_dir/users/$user_id/.weazl-archives/.archive-stale.tmp"
stop_node

mkdir -p "$restored_dir"
cp -a "$data_dir"/. "$restored_dir"/
start_node "$restored_dir" "$root/restore.log"
restore_jar="$root/restore-cookies.txt"
curl -fsS -c "$restore_jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"username":"recovery","password":"recovery-pass"}' \
  "http://127.0.0.1:$base_port/api/login" >/dev/null
curl -fsS -b "$restore_jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"passphrase":"recovery-vault"}' "http://127.0.0.1:$base_port/api/unlock" >/dev/null
curl -sS -b "$restore_jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$base_port/api/library/archive?id=stale-check" >/dev/null || true
curl -fsS -b "$restore_jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$base_port/api/library?path=payload.bin" -o "$root/restored.bin"
restored_hash="$(sha256sum "$root/restored.bin" | awk '{print $1}')"
[[ "$restored_hash" == "$expected" ]] || { echo "restored hash mismatch" >&2; exit 1; }
[[ ! -e "$restored_dir/users/$user_id/.weazl-archives/.archive-stale.tmp" ]] || { echo "stale archive temp was not cleaned" >&2; exit 1; }

echo "recovery smoke: live upload, filesystem restore, login, unlock, download, and stale archive cleanup passed"
