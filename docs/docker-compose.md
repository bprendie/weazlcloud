# Docker Compose deployment

`deploy/compose.yaml` builds the API and private photo worker from the same
checkout and selects the same `WEAZLCLOUD_IMAGE` tag for both. Defaults are a
Compose-managed data volume, automatic API preview admission, and a sidecar
limited to one CPU and 2 GiB. No host ports are published by default.

## Configure and start

From the repository root:

```sh
cp deploy/compose.env.example deploy/compose.env
# Edit deploy/compose.env for your host before starting.
docker compose --env-file deploy/compose.env -f deploy/compose.yaml config --quiet
docker compose --env-file deploy/compose.env -f deploy/compose.yaml up --build -d
docker compose --env-file deploy/compose.env -f deploy/compose.yaml ps
```

`deploy/compose.env` is ignored by Git. Do not put vault/account passwords in
Compose or its environment file; create and unlock local accounts in the desk.
Shell environment values take precedence over the environment file.

The default data mount is `weazlcloud-data:/data`. To use a host filesystem,
set `WEAZLCLOUD_DATA_SOURCE` to its absolute directory, for example
`/exports/dockervolume/weazlcloud`. The service runs as UID/GID `7272:7272`;
a new directory must be writable by that identity. Preserve the existing mount
and permissions when upgrading an installation. Vaults, accounts, catalogs,
originals, previews, archives and temporary Restic packs live under `/data`.
The worker receives only the IPC volume and ephemeral scratch storage.

The desk, grab and WebDAV listeners are internal ports 7272, 7273 and 7274.
Attach the API service to your existing reverse-proxy network or use an operator
Compose override for host routing. Desk and grab need separate routes; the grab
listener cannot unlock a vault. WebDAV can stay private. Set
`WEAZLCLOUD_SECURE_COOKIES=true` behind desk HTTPS, and configure the grab
hostname through the administrator's settings.

## Preview settings

| Setting | Behavior |
| --- | --- |
| `WEAZLCLOUD_IMAGE` | Shared API/worker image tag; defaults to `weazlcloud:local`. |
| `WEAZLCLOUD_PHOTO_CPUS` | Hard worker CPU limit; defaults to `1.0`. Choose within your deployment's photo CPU allowance. |
| `WEAZLCLOUD_PHOTO_MEMORY_LIMIT` | Hard worker memory limit, including codec children; defaults to `2g`. |
| `WEAZLCLOUD_PREVIEW_RENDERER` | `auto`, `go`, or `turbo`; passed to both services. Native JPEG SIMD is detected at runtime. |
| `WEAZLCLOUD_PHOTO_SCHEDULE` | `quiet`, `balanced`, or `fast`; unset uses `balanced`. |
| `WEAZLCLOUD_PREVIEW_BACKGROUND_WORKERS`, `WEAZLCLOUD_PREVIEW_TOTAL_WORKERS`, `WEAZLCLOUD_PREVIEW_SOURCE_READERS` | Optional positive integers; detected CPU ceilings still apply. |
| `WEAZLCLOUD_PREVIEW_MEMORY_BYTES` | Optional positive byte allowance; cannot exceed detected preview memory admission. |
| `WEAZLCLOUD_PREVIEW_OWNER_BYTES`, `WEAZLCLOUD_PREVIEW_OWNER_FILES`, `WEAZLCLOUD_PREVIEW_NODE_BYTES` | Optional positive integer cache limits; byte values are bytes. Unset byte caps retain adaptive disk sharing. |
| `WEAZLCLOUD_PREVIEW_BUNDLE`, `WEAZLCLOUD_PREVIEW_READER` | Set either to `off` for its forward fallback. |

Leave optional numeric settings absent for automatic sizing. Assigning an empty
value is invalid. These keys are passed through from the shell or environment
file only when defined. Preview limits control generated data and processing;
they do not create per-user storage quotas.

API/source-reader admission and the worker's cgroup limits are separate.
Increasing worker limits alone does not increase the API's detected allowance.
For a larger installation, preserve its existing API and worker CPU/RAM limits
in the host override and tune concurrency within those allocations. Raising
limits does not resume a manually paused preparation job.

## Upgrade an existing node

Keep the host override, environment file, data/IPC mounts, storage backend,
resource limits and reverse-proxy configuration. Compare the resolved Compose
configuration before changing services. Keep the current image and a consistent
backup, and settle or checkpoint active imports/transfers before rollout.

A tag can be selected without rebuilding it:

```sh
# The selected image must already exist locally or be available to pull.
WEAZLCLOUD_IMAGE=weazlcloud:release-YOUR_REVISION \
  docker compose --env-file deploy/compose.env -f deploy/compose.yaml \
  up -d --no-build
```

The API has a 90-second stop grace for its bounded 60-second drain. A successful
unlocked drain exports photo job journals and preserves ready download ZIPs.
Verify clean shutdown before treating a data copy as a consistent restore point.
After startup, check health, login/unlock, Library, Photos, a preview and the
existing preparation pause state. Do not use `down --volumes` on retained data.

See [release and rollback](release.md) and the
[Photos recovery runbook](photos-release-runbook.md) for verification and catalog
compatibility. Changing a mount or backend is a separate migration.
