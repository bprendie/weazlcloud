# Native Restic batch writer

Build with `go build -o restic-writer .` and run `restic-writer -repo PATH -workers 2`.
Uses Restic v0.18.0 internal packages under its import namespace, like the native
reader. Local repository only; the repository must already exist. Workers may be
1 or 2 (default 2); blob and tree worker pools each have two workers.

One invocation writes one stock Restic snapshot:

* FD 3: repository password as lowercase hex text, optionally followed by a
  newline, then EOF. This is the password itself, not decoded hex bytes. Maximum
  256 hex characters; no password argument or environment variable is read.
* stdin: one JSON object, maximum 64 KiB including whitespace, then EOF:
  `{"version":1,"files":[{"name":"0123456789abcdef0123456789abcdef","size":3,"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}]}`
* Files: 1–8 unique lowercase 32-hex opaque names, required nonnegative int64
  sizes totaling at most 64 MiB, required lowercase 64-hex SHA-256 values.
  Unknown JSON fields are rejected. Names become root-level snapshot files.
* FD `4+i`: inherited pipe containing the exact raw bytes for file `i`, then EOF.
  These must be separate pipes. Feed concurrently: archiving can reorder reads
  and applies backpressure. Close all unused copies of write ends. The example
  above requires `abc` on FD 4.
* Success: stdout contains only `{"snapshot_id":"<64 lowercase hex>"}` and a
  newline; exit 0. No progress or ready message is emitted.
* Failure: nonzero exit, generic `batch write failed` stderr, no success JSON.
  Invalid CLI arguments exit 2; other failures exit 1.

The helper authenticates, takes a renewable nonexclusive repository lock, loads
the index, and uses Restic's archiver and repository chunker polynomial. Each
file routes through a synthetic `fs.Reader`; length and SHA-256 are checked as
it streams, including actual EOF, before the archiver may save the snapshot.
Empty files are supported. No whole-original buffering or plaintext staging is
used. Chunk buffers and encrypted pack writing use Restic's bounded worker pools;
repository index memory still scales with repository size.

Any failed source aborts the entire batch and closes sibling pipes, including
blocked reads. SIGINT/SIGTERM and lock loss also cancel reads. No files are skipped
and no partial snapshot is saved for a source failure. Encrypted unreferenced
packs may remain after an aborted attempt, as with interrupted stock backups.
The caller should retry batch failures transiently, splitting batches or retrying
singly to isolate persistently bad inputs without preventing good siblings from
progressing. The caller supplies its own deadline for a source that never closes.
As with any CLI, losing the process/output after snapshot save can leave a
committed snapshot without an acknowledgement; retries are not idempotent.

Run `go test ./...` (stock `restic` on PATH enables integration coverage).
Integration tests generate streams, use eight inherited data pipes, compare
stock `restic dump` hashes and sizes, run `restic check --read-data`, reject short,
overlong and checksum-bad streams without publishing a snapshot, exercise a
blocked sibling, handle SIGTERM during blocked reads, and retry a good sibling.
The fixture builds the helper with race instrumentation. Source read errors and manifest limits
also have unit coverage. The stock CLI test is skipped when it is unavailable.

Restic's BSD-2-Clause license is included in `LICENSE.restic`.
