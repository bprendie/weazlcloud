#!/usr/bin/env bash
set -euo pipefail

image="${WEAZLCLOUD_IMAGE:-weazlcloud:2026.09.21}"
storage_backend="${WEAZLCLOUD_SMOKE_STORAGE_BACKEND:-restic}"
nonce="${WEAZLCLOUD_SMOKE_ID:-$(date +%s)-$$}"
name="weazlcloud-phase6-smoke-$nonce"
desk_port="${WEAZLCLOUD_CONTAINER_PORT:-19272}"
share_port="$((desk_port + 1))"
drive_port="$((desk_port + 2))"
root="$(mktemp -d)"
volume_name="weazlcloud-phase6-smoke-data-$nonce"
jar="$root/cookies.txt"
payload="$root/sample.svg"
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm "$volume_name" >/dev/null 2>&1 || true; rm -rf "$root"' EXIT

printf '<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8" fill="purple"/></svg>\n' >"$payload"
expected="$(sha256sum "$payload" | awk '{print $1}')"
docker run --detach --name "$name" \
  --publish "127.0.0.1:$desk_port:7272" \
  --publish "127.0.0.1:$share_port:7273" \
  --publish "127.0.0.1:$drive_port:7274" \
  --env WEAZLCLOUD_DATA=/data \
  --env WEAZLCLOUD_DESK_ADDR=:7272 \
  --env WEAZLCLOUD_SHARE_ADDR=:7273 \
  --env WEAZLCLOUD_DRIVE_ADDR=:7274 \
  --env WEAZLCLOUD_PUBLIC_BASE=https://grab.test \
  --env "WEAZLCLOUD_STORAGE_BACKEND=$storage_backend" \
  --env HOME=/data \
  --env TMPDIR=/data/tmp \
  --volume "$volume_name:/data" \
  "$image" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$desk_port/live" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done
curl -fsS "http://127.0.0.1:$desk_port/ready" >/dev/null
curl -fsS -c "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"username":"container","password":"container-pass","vault_passphrase":"container-vault","confirm":"container-vault"}' \
  "http://127.0.0.1:$desk_port/api/bootstrap" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"passphrase":"container-vault"}' "http://127.0.0.1:$desk_port/api/unlock" >/dev/null
maintenance="$(curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/admin/maintenance")"
grep -Fq 'expired-upload-sessions' <<<"$maintenance"
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' --upload-file "$payload" \
  "http://127.0.0.1:$desk_port/api/library?path=sample.svg" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/library?path=sample.svg" -o "$root/download.svg"
[[ "$(sha256sum "$root/download.svg" | awk '{print $1}')" == "$expected" ]]
curl -fsS -u 'container:container-pass' --upload-file "$payload" \
  "http://127.0.0.1:$drive_port/dav.svg" >/dev/null
curl -fsS -u 'container:container-pass' "http://127.0.0.1:$drive_port/dav.svg" -o "$root/webdav.svg"
[[ "$(sha256sum "$root/webdav.svg" | awk '{print $1}')" == "$expected" ]]
resumable="$root/resumable.bin"
printf 'resumable upload stays intact across process restart\n' >"$resumable"
resumable_hash="$(sha256sum "$resumable" | awk '{print $1}')"
resumable_size="$(wc -c <"$resumable" | tr -d ' ')"
first_size="$((resumable_size / 2))"
head -c "$first_size" "$resumable" >"$root/first.part"
tail -c +"$((first_size + 1))" "$resumable" >"$root/second.part"
upload_json="$(curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d "{\"path\":\"resumed.bin\",\"size\":$resumable_size,\"hash\":\"$resumable_hash\"}" \
  "http://127.0.0.1:$desk_port/api/uploads")"
upload_id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$upload_json")"
first_hash="$(sha256sum "$root/first.part" | awk '{print $1}')"
curl -fsS -b "$jar" -X PATCH -H 'X-Weazl-Desk: 1' -H 'Upload-Offset: 0' \
  -H "Upload-Chunk-SHA256: $first_hash" --data-binary @"$root/first.part" \
  "http://127.0.0.1:$desk_port/api/uploads/$upload_id" >/dev/null
reserved_before="$(curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' "http://127.0.0.1:$desk_port/api/quota" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reserved"])')"
[[ "$reserved_before" -gt 0 ]]
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/library?path=sample.svg&preview=1" | grep -Fq '<svg'
grab="$(curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"path":"sample.svg","kind":"file","gate":"open","expiry":"90m","grabs":4}' \
  "http://127.0.0.1:$desk_port/api/capsules")"
id="$(python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])' <<<"$grab")"
curl -fsS "http://127.0.0.1:$share_port/g/$id/meta" >/dev/null
curl -fsS "http://127.0.0.1:$share_port/g/$id/file" -o "$root/grab.svg"
[[ "$(sha256sum "$root/grab.svg" | awk '{print $1}')" == "$expected" ]]
docker restart "$name" >/dev/null
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$desk_port/live" >/dev/null 2>&1; then break; fi
  sleep 0.25
done
curl -fsS "http://127.0.0.1:$desk_port/ready" >/dev/null
curl -fsS -c "$jar" -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"username":"container","password":"container-pass"}' "http://127.0.0.1:$desk_port/api/login" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
  -d '{"passphrase":"container-vault"}' "http://127.0.0.1:$desk_port/api/unlock" >/dev/null
offset="$(curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' "http://127.0.0.1:$desk_port/api/uploads/$upload_id" | python3 -c 'import json,sys; print(json.load(sys.stdin)["offset"])')"
[[ "$offset" == "$first_size" ]]
reserved_after="$(curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' "http://127.0.0.1:$desk_port/api/quota" | python3 -c 'import json,sys; print(json.load(sys.stdin)["reserved"])')"
[[ "$reserved_after" == "$reserved_before" ]]
second_hash="$(sha256sum "$root/second.part" | awk '{print $1}')"
curl -fsS -b "$jar" -X PATCH -H 'X-Weazl-Desk: 1' -H "Upload-Offset: $offset" \
  -H "Upload-Chunk-SHA256: $second_hash" --data-binary @"$root/second.part" \
  "http://127.0.0.1:$desk_port/api/uploads/$upload_id" >/dev/null
curl -fsS -b "$jar" -X POST -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/uploads/$upload_id/finalize" >/dev/null
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/library?path=resumed.bin" -o "$root/resumed.out"
[[ "$(sha256sum "$root/resumed.out" | awk '{print $1}')" == "$resumable_hash" ]]
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/library?path=sample.svg" -o "$root/restarted.svg"
[[ "$(sha256sum "$root/restarted.svg" | awk '{print $1}')" == "$expected" ]]
curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
  "http://127.0.0.1:$desk_port/api/library?path=sample.svg&preview=1" | grep -Fq '<svg'
curl -fsS -u 'container:container-pass' "http://127.0.0.1:$drive_port/dav.svg" -o "$root/restarted-webdav.svg"
[[ "$(sha256sum "$root/restarted-webdav.svg" | awk '{print $1}')" == "$expected" ]]
curl -fsS "http://127.0.0.1:$share_port/g/$id/file" -o "$root/restarted-grab.svg"
[[ "$(sha256sum "$root/restarted-grab.svg" | awk '{print $1}')" == "$expected" ]]
echo "container smoke ($storage_backend): Desk/WebDAV, resumable finalize, preview, sealed grab, and reads survived restart"

if [[ "$storage_backend" == "restic" ]]; then
  docker stop "$name" >/dev/null
  dry_run="$(docker run --rm --volumes-from "$name" --env WEAZLCLOUD_DATA=/data "$image" -migrate dry-run)"
  python3 -c 'import json,sys; report=json.load(sys.stdin); assert report["eligible_files"] >= 3 and not report["unassigned_legacy_catalog"], report' <<<"$dry_run"
  migrated="$(docker run --rm --volumes-from "$name" --env WEAZLCLOUD_DATA=/data "$image" -migrate start)"
  python3 -c 'import json,sys; report=json.load(sys.stdin); assert report["migrated_files"] >= 3 and report["remaining_files"] == 0, report' <<<"$migrated"
  verified="$(docker run --rm --volumes-from "$name" --env WEAZLCLOUD_DATA=/data "$image" -migrate verify)"
  python3 -c 'import json,sys; report=json.load(sys.stdin); assert report["migrated_files"] >= 3 and report["remaining_files"] == 0, report' <<<"$verified"
  docker rm "$name" >/dev/null
  docker run --detach --name "$name" \
    --publish "127.0.0.1:$desk_port:7272" \
    --publish "127.0.0.1:$share_port:7273" \
    --publish "127.0.0.1:$drive_port:7274" \
    --env WEAZLCLOUD_DATA=/data \
    --env WEAZLCLOUD_DESK_ADDR=:7272 \
    --env WEAZLCLOUD_SHARE_ADDR=:7273 \
    --env WEAZLCLOUD_DRIVE_ADDR=:7274 \
    --env WEAZLCLOUD_PUBLIC_BASE=https://grab.test \
    --env WEAZLCLOUD_STORAGE_BACKEND=shared-experimental \
    --env HOME=/data \
    --env TMPDIR=/data/tmp \
    --volume "$volume_name:/data" \
    "$image" >/dev/null
  for _ in $(seq 1 60); do
    if curl -fsS "http://127.0.0.1:$desk_port/live" >/dev/null 2>&1; then break; fi
    sleep 0.25
  done
  curl -fsS "http://127.0.0.1:$desk_port/ready" >/dev/null
  curl -fsS -c "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
    -d '{"username":"container","password":"container-pass"}' "http://127.0.0.1:$desk_port/api/login" >/dev/null
  curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' -H 'Content-Type: application/json' \
    -d '{"passphrase":"container-vault"}' "http://127.0.0.1:$desk_port/api/unlock" >/dev/null
  curl -fsS -b "$jar" -H 'X-Weazl-Desk: 1' \
    "http://127.0.0.1:$desk_port/api/library?path=sample.svg" -o "$root/migrated.svg"
  [[ "$(sha256sum "$root/migrated.svg" | awk '{print $1}')" == "$expected" ]]
  curl -fsS "http://127.0.0.1:$share_port/g/$id/file" -o "$root/migrated-grab.svg"
  [[ "$(sha256sum "$root/migrated-grab.svg" | awk '{print $1}')" == "$expected" ]]
  echo "container migration smoke: Restic volume migrated, verified, and reopened in mixed mode"
fi
