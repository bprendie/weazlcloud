# Persistent metadata source reader

This small adapter pins Restic **v0.18.0**, matching the CLI in the image. It uses
Restic's authenticated repository/index/blob implementation under Restic's
internal import namespace. It is WeazlCloud code, not an official Restic tool.
Restic is BSD-2-Clause licensed; see `LICENSE.restic`.

A job opens a local repository once, takes a refreshed shared lock, loads its
index, then accepts bounded JSON requests over inherited pipes. The password
arrives only on descriptor 3. Up to 16 readers resolve exact immutable snapshot
IDs and object paths; each response is at most 4 MiB. Source size and complete
small-file SHA-256 are checked again against the owner's catalog by the parent.
No metadata plaintext is staged on disk. Requests never mutate repository data;
the only repository writes are the normal shared lock and its refresh/removal.

The parent owns authorization, reference holds, resource admission, checkpoints,
and catalog commits. The helper exits on job cancellation/vault lock/shutdown;
SIGTERM permits lock cleanup, with a five-second kill fallback. Newly uploaded
packs may not be in an open index, so the parent falls back to the normal Restic
reader for a missed source. Builds without this helper, shared-object backends,
and very small memory allocations retain the existing source reader.

`make metadata-reader` builds and checks the helper. `make check` includes real
Restic protocol, prefix, concurrency, failure isolation, and lock cleanup tests.
Docker builds it independently; the main Go module does not import Restic's
private packages. Upgrade its pinned dependency together with the CLI and run
the parity tests before release.
