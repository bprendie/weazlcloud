# Release and rollback

Build a checked binary and image tagged with the source revision:

```sh
make check
release_revision=$(git rev-parse HEAD)
release_image=weazlcloud:release-$(git rev-parse --short=12 HEAD)
make build VERSION="$release_revision"

docker build --file deploy/Dockerfile \
  --label "org.opencontainers.image.revision=$release_revision" \
  --tag "$release_image" .

WEAZLCLOUD_IMAGE="$release_image" bash scripts/container-smoke.sh
WEAZLCLOUD_IMAGE="$release_image" WEAZLCLOUD_SMOKE_STORAGE_BACKEND=shared-experimental \
  bash scripts/container-smoke.sh
```

The Docker build generates the embedded desk UI from `mockup-ui`.
`internal/desk/ui` is a build artifact and is intentionally not committed.
The [Compose guide](docker-compose.md) documents the shared API/worker image tag,
optional environment file, host mounts and resource settings. See the
[October 2 selection release](release-2026-10-02.md) for deployed behavior and checks.

Before a production rollout, preserve the current image/config and a consistent
data-volume backup. Settle or checkpoint active transfers/imports; keep the host
Compose override and Traefik configuration separate from repository defaults.
Build and smoke the intended image with disposable data before replacing services.
Keep the existing storage backend, vault/data/IPC mounts and resource budgets.

For rollback, first unlock and cleanly drain the current service so its encrypted
photo job journal exports into the compatible snapshot. Confirm a successful
shutdown, then select a compatible previous image for **both** services against
the preserved mounts. An older writer must understand the current catalog fields,
including individual hidden assets, albums and mobile pair metadata. Use a forward
fix or isolated recovery copy when compatibility is uncertain; do not discard
journals or authoritative metadata to make an old image start. A data rollback
requires a consistent restore point and intentional handling of newer changes.
See the [Photos recovery runbook](photos-release-runbook.md).

Check `/live`, `/ready`, login/unlock, Library, Photos, upload/download, previews,
Archive/Hidden restore and a disposable grab after rollout or rollback. Verify a
manually paused preparation job remains paused and original hashes are unchanged.

Operational signals are deliberately small and secret-free:

- `/live` reports that the process is serving.
- `/ready` reports whether the configured data directory is available.
- HTTP logs record method, safe route, status, and duration. Capsule IDs are redacted from request paths.
- Archive jobs record status, file count, byte count, and duration without capability IDs or error payloads.

Preview working-memory reservations are process-wide. Waiting foreground renders
get priority over background preparation, but a visible request joining a
coalesced job waiting for a background slot can promote that job. An already
active native render is not preempted. Shared-store
writes reuse one zstd encoder sequentially within each manifest without changing
the on-disk format. Synthetic local codec benchmarks improved time and allocation;
production upload gains remain unmeasured. Adaptive pressure feedback, fair
scheduling among owners, node-wide accounting for retained catalog/index RAM, and large-host
mixed-load measurements remain follow-up work. See the
[September 28 responsiveness workbook](../responsiveness_workbook_2026-09-28.md)
for the R5/R6 evidence and remaining gates.
