# Release and rollback

Build a versioned binary and image from a clean checkout:

```sh
make check
make build VERSION=2026.09.21

docker build --file deploy/Dockerfile \
  --tag weazlcloud:2026.09.21 .

WEAZLCLOUD_IMAGE=weazlcloud:2026.09.21 make smoke-container
```

The Docker build generates the embedded desk UI from `mockup-ui`. `internal/desk/ui` is a build artifact and is intentionally not committed.

Before a production rollout, preserve the current image tag, confirm a recent data-volume backup, and run `make smoke-recovery` with disposable data plus `make smoke-container` against the built image. Keep the host Compose override and Traefik configuration separate from the repository defaults.

For rollback, stop the service, restore the previous image tag, and start it against the same data volume. Do not restore a data volume to an older image until its catalog and vault format are known to be compatible. Check `/live`, `/ready`, login, unlock, upload, download, preview, and a disposable grab after the rollback.

Operational signals are deliberately small and secret-free:

- `/live` reports that the process is serving.
- `/ready` reports whether the configured data directory is available.
- HTTP logs record method, safe route, status, and duration. Capsule IDs are redacted from request paths.
- Archive jobs record status, file count, byte count, and duration without capability IDs or error payloads.

Preview working-memory reservations are process-wide. Waiting foreground renders
get priority over background preparation, but a visible request joining an
already-running coalesced background render is not promoted in place. Shared-store
writes reuse one zstd encoder sequentially within each manifest without changing
the on-disk format. Synthetic local codec benchmarks improved time and allocation;
production upload gains remain unmeasured. Adaptive pressure feedback, fair
scheduling among owners, node-wide accounting for retained catalog/index RAM, and large-host
mixed-load measurements remain follow-up work. See the
[September 28 responsiveness workbook](../responsiveness_workbook_2026-09-28.md)
for the R5/R6 evidence and remaining gates.
