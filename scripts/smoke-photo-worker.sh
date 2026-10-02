#!/usr/bin/env bash
set -euo pipefail
image="${WEAZLCLOUD_IMAGE:-weazlcloud:smoke}"
name="weazl-photo-worker-smoke-$$"
root="$(mktemp -d)"
cleanup(){ docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$root"; }
trap cleanup EXIT
# Render probes contain only the compiled-in generated blank HEIC and generated
# two-second blue video. The worker has no live data volume or network.
args=(--cpus 2 --memory 4g --network none --read-only --cap-drop ALL --security-opt no-new-privileges:true
 --tmpfs /tmp:size=64m,mode=1777 --tmpfs /data:size=1m,mode=700,uid=7272,gid=7272
 --tmpfs /run/weazlcloud-photo:size=1m,mode=700,uid=7272,gid=7272
 -e HOME=/tmp -e WEAZLCLOUD_PHOTO_WORKER_SOCKET=/run/weazlcloud-photo/worker.sock
 --entrypoint /usr/local/bin/weazl-photo-worker)
if docker run --rm "${args[@]}" -e TMPDIR=/data/tmp "$image" > "$root/invalid.log" 2>&1; then
 echo 'FAIL: invalid worker scratch was accepted';exit 1
fi
if ! rg -q 'renderer configuration is invalid' "$root/invalid.log";then cat "$root/invalid.log";exit 1;fi
docker run -d --name "$name" "${args[@]}" -e TMPDIR=/tmp "$image" >/dev/null
for attempt in {1..50};do
 if docker exec "$name" /usr/local/bin/weazl-photo-worker -ready;then break;fi
 sleep .1
done
docker exec "$name" /usr/local/bin/weazl-photo-worker -probe
echo 'PASS: isolated HEIC 320/1280 bundle and PNG, truthful startup failure for absent scratch'
