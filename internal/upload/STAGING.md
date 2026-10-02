# Sequential upload staging compatibility

New owner-backed `upload.Manager` sessions use format 2. `ResourceFor` must resolve
the owner's existing vault (the production desk registry already does). The
`.json` filename remains discoverable by lifecycle cleanup, but its contents are
`WZU2\n` followed by a vault-encrypted manifest. Paths, sizes, hashes, session key,
progress and segment lengths are private. No new cipher is introduced.

Each ordered PATCH writes `cryptox.StreamFileWriter` ciphertext to `.chunk`, using
a per-session/per-index derived key. After length and SHA-256 verification and
fsync, an encrypted pending marker is persisted. Recovery promotes that file to
`<id>.part/<index>.enc`, fsyncs directories, then advances the manifest once.
Interrupted unaccepted chunks may be discarded; accepted segments are never
removed to "migrate" a session. Full-component verification and commit read one
authenticated segment at a time without concatenating or spooling plaintext.
Session IDs, ordered offsets, maximum chunk size, full/chunk hash contracts,
24-hour inactivity and idempotent completed-session responses stay compatible.

Raw manifests and flat `.part`/`.chunk` files are detected as format 0 and remain
on the original read/resume/finalize path until completion/cancel/expiry. There is
no eager conversion and no deletion of pending legacy bytes. Raw legacy data
therefore remains plaintext until drained. Resolver-less managers retain this
legacy-only behavior for existing adapters/tests; do not use one for new
production uploads that require encrypted staging.

The session gate covers reads, incoming PATCH, finalize, cancellation, sweeping
and owner deletion. A sweeper cannot delete a leased transfer/commit. Owner
account lifecycle must continue calling `DeleteOwner`, which now also removes
segment directories. Owner-vault lock rejects private v2 state access; no keys or
decrypted manifests are cached in Manager. Vault rekey retains the DEK, so the
wrapped manifests remain readable.

Encrypted sessions keep the full conservative source/commit workspace reservation
and
add 8 KiB per PATCH for frames, allocation and manifest growth. On restart,
locked encrypted manifests cannot reveal their declared sizes, so reservation
restoration and expiry are deferred until unlock. Existing encrypted allocations
remain visible to disk-space quota checks. The next authenticated status or upload operation
restores its reservation, including after a locked startup. Live admission and
restart restoration use the same full-size and accepted-segment formula. The
parent can call `RestoreOwner(user)` after unlock to restore all owner sessions
before the next status request; invoke it outside users publication guards. Locked owners can defer expiry;
this is intentional retention rather than an unsafe encrypted-state sweep.

This adapter encrypts **upload.Manager staging**. Existing generic Library
finalize callbacks can still use their own legacy staging implementation; this
change does not convert that separate storage layer. The mobile backup reader
and parent's independent-parts transport avoid that plaintext path.

Validation: encrypted round-trip/restart/no-plaintext checks; pending-marker
recovery before/after rename; failed partial transfer; tampering fails before
commit; locked-state retention; zero-byte upload; mixed raw drain and encrypted
new sessions; all existing upload.Manager tests.

## Changed files

Existing files: `manager.go`, `session.go`, `operations.go`, `lifecycle.go`,
`maintenance.go`.

New files: `encrypted_manifest.go`, `encrypted_append.go`,
`encrypted_finalize.go`, `encrypted_recovery.go`, `legacy_recovery.go`,
`encrypted_test.go`, `encrypted_fault_test.go`, `encrypted_quota_test.go`, `encrypted_restore_test.go`, `STAGING.md`.

`List` returns valid partial results and joins per-session errors instead of
stopping at a corrupt, future-format or over-quota neighbor. `RestoreOwner`
therefore restores independent valid reservations before reporting an aggregate
warning. Parent unlock handling may log that warning without failing unlock.
Bad-neighbor manifests are preserved; no schema downgrade is attempted.
