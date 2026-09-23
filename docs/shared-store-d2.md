# D2 disposable shared-object prototype

Date: September 23, 2026
Status: experimental package only; normal library reads and writes still use Restic.

`internal/sharedstore` is a standalone, single-process prototype for proving whole-file dedupe and its ownership/recovery rules. No server route, configuration flag, catalog format, account lifecycle worker, WebDAV handler, preview, ZIP, or capsule uses it. Do not point it at the production data volume or treat it as a migration tool.

## Private state and identity

Given a configured data root, the prototype creates `shared-index/`, `shared-objects/`, and `shared-staging/` below that root. Directories are 0700; key, SQLite, WAL/SHM, stage, and ciphertext files are 0600. `shared-index/node.keys` contains independent random 256-bit fingerprint and key-wrapping keys. Losing it makes indexed shared objects unreadable; a recovery backup must include it with the SQLite database and object files. A user's vault/recovery kit deliberately has no copy of these node-wide keys.

The SQLite index contains opaque object IDs, HMAC fingerprints, lengths, encrypted node key envelopes, keyed-pseudonym owner/entry/revision tuples, encrypted per-owner key envelopes, and operation state. It contains no path, username, MIME type, or raw content hash. SQLite uses foreign keys, WAL, FULL synchronous commits, a 5-second busy timeout, and one bounded connection. Unknown future schema versions fail startup. Startup removes incomplete stage files and unindexed object candidates; a missing indexed object stops startup rather than being treated as empty storage.

An object fingerprint is HMAC-SHA-256 under the node fingerprint key over a versioned domain, actual server-read length, and SHA-256 of the actual bytes. No client digest is accepted. Each object gets a random 256-bit AES key; the node wrapping key protects an index copy, while every owner gets a separate `vault.Wrap` envelope containing the same data key and its exact owner, entry, revision, and object tuple. The envelope uses that user's unlocked vault DEK. A copied object ID or reference does not authorize a read: the index must contain the exact live owner tuple, the vault must be unlocked, and both key envelopes must agree.

## Object framing

Format version 1 starts with `WZLOBJ01` and a fresh 32-bit nonce prefix. It stores 1 MiB maximum AES-256-GCM records. Each record authenticates its kind, monotonically increasing 64-bit sequence, plaintext length, file header, and opaque object ID. A final authenticated record binds total plaintext bytes and data-frame count. Nonces are the four-byte prefix followed by the big-endian sequence. Readers reject unknown versions/kinds, wrong order, bad tags, truncation, missing/incorrect final record, and trailing bytes. A frame is authenticated before that frame's plaintext is written. The writer and reader use bounded frame buffers and stream; upload staging and encrypted payloads stay on the configured data volume.

This framing is a D2 whole-object experiment. It is not the final chunk/manifest format, a claim of server-blind encryption, or a production security review. D3 must decide whether to retain, replace, or wrap it when adding content-defined chunks.

## Write and recovery API

1. `Prepare` requires an owner, unlocked vault, stable entry ID and revision. It streams to private staging while measuring length and hashing actual bytes, rejects a mismatched declared length, then checks the keyed index.
2. A ready duplicate is fully decrypted/authenticated before adoption. For new content, an opaque candidate is encrypted to a same-volume temporary file, synced, renamed, and its directory synced before an SQLite uniqueness insert. Concurrent writers that lose the unique insert adopt and verify the winner. A crash before index insertion leaves only an unindexed candidate; startup removes it. A ready object with no owner can be safely reused.
3. `Prepare` writes an owner-wrapped pending reference and a durable operation row, then returns the reference. The caller must put that immutable reference and operation ID in its encrypted owner catalog and durably publish that catalog.
4. `MarkPublished` records that catalog boundary. `Commit` promotes the reference to live and is idempotent. It rejects an older revision after a newer revision is live. The caller serializes catalog mutations and supplies the expected revision; it must resolve a failed/stale publication against that catalog before retrying. If the process stops, the caller inspects its encrypted catalog and calls `Recover(opID, published)`: true finalizes the already-published catalog reference; false aborts an unpublished pending owner reference. Recovery decisions must come from the owner catalog, never an HTTP/client assertion.
5. `Read` streams only an exact live tuple. `Release` removes just one owner reference. D2 does not physically collect unreferenced ready objects; safe collection, holds, Trash integration, and account deletion are later work.

The current failure hook and tests exercise errors after ciphertext sync, object rename/index, prepared reference, catalog publication, and final commit. Staging is deliberately removed after the index owns a complete object. Operations are not resumed from a partial upload; the caller retries with a new operation ID. A publication failure is retried using the original operation ID from the encrypted catalog.

## Verification

Run `make smoke-sharedstore`. It builds a disposable named Docker volume, mounts the source tree read-only, and puts all test data, temporary files, SQLite files, and Go caches in that volume. The volume is deleted on exit. Tests cover same-content cross-owner and simultaneous writes, one physical object, independent owner authorization, admin/no-reference denial, expected-length rejection, authenticated frame corruption/trailing data, restart recovery around catalog publication, lock/rekey/stored node unlock, and releasing one owner's reference while the other remains readable.

This evidence proves only the tested small whole-file cases. Account-password change and vault rekey are exercised separately; the former leaves shared access intact through the stable account ID and vault DEK. It does not establish production readiness, large-scale performance, power-loss behavior on every filesystem, deletion/garbage-collection safety, backup restore, or integration with live WeazlCloud flows.
