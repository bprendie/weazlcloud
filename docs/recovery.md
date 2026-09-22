# Recovery

WeazlCloud stores the catalog, user records, vault keys, library repository, capsules, and generated download archives below the configured data directory. A filesystem backup must include the complete directory while the node is stopped, or use a snapshot that guarantees a consistent view.

The recovery kit is an encrypted convenience export for the vault and node configuration. It does not replace a backup of the library repository. The vault passphrase is required; there is no reset path.

## Routine recovery rehearsal

Run the disposable end-to-end check from the repository root:

```sh
make smoke-recovery
```

The check starts a temporary node, creates a local account, unlocks its vault, uploads a payload, stops the node, copies the data directory to a fresh location, starts from that copy, logs in again, unlocks, downloads the payload, compares its SHA-256 hash, and verifies stale archive temporary files are removed.

## Volume recovery

1. Stop the container and preserve the old data volume.
2. Copy the complete `/data` contents into a fresh volume with ownership readable and writable by UID/GID `7272:7272`.
3. Start the same image version against the restored volume.
4. Check `/live` for process health and `/ready` for data-volume readiness.
5. Log in, unlock a vault, compare representative file hashes, and confirm a preview and disposable grab.
6. Keep the old volume untouched until the restored node has passed those checks.

Catalog-save atomicity, vault rekey atomicity, staged-upload recovery, and archive cleanup are covered by the Go tests. Run `make check` before treating a release as recoverable.
