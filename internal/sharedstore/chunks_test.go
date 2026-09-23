package sharedstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"os"
	"testing"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestChunkCodecKeepsRawNoiseAndCompressesRepetition(t *testing.T) {
	noise := make([]byte, 1<<20)
	if _, err := rand.New(rand.NewSource(9)).Read(noise); err != nil {
		t.Fatal(err)
	}
	stored, encoding, err := encodeChunk(noise)
	if err != nil || encoding != chunkRaw || len(stored) != len(noise) {
		t.Fatalf("random chunk codec=%s size=%d err=%v", encoding, len(stored), err)
	}
	repeated := bytes.Repeat([]byte("weazlcloud chunk codec "), 50000)
	stored, encoding, err = encodeChunk(repeated)
	if err != nil || encoding != chunkZstd || len(stored) >= len(repeated) {
		t.Fatalf("compressible chunk codec=%s size=%d err=%v", encoding, len(stored), err)
	}
	var decoded bytes.Buffer
	if _, err = decodeChunk(encoding, stored, int64(len(repeated)), &decoded); err != nil || !bytes.Equal(decoded.Bytes(), repeated) {
		t.Fatalf("compressed chunk roundtrip failed: %v", err)
	}
	if _, err = decodeChunk(chunkZstd, []byte("not a zstd frame"), 12, io.Discard); err == nil {
		t.Fatal("invalid compressed frame was accepted")
	}
}

func TestChunkParametersArePinnedToTheSharedIndex(t *testing.T) {
	root := testRoot(t)
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE format_settings SET value='unknown-rabin-layout' WHERE setting='chunking'`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(root, Options{}); !errors.Is(err, ErrFormat) {
		t.Fatalf("unknown persisted chunk format accepted: %v", err)
	}
}

func TestChunkedManifestReadsAndModifiedRegionReuse(t *testing.T) {
	root := testRoot(t)
	ctx := context.Background()
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	alice, bob := testVault(t, root, "chunk-alice"), testVault(t, root, "chunk-bob")
	data := make([]byte, 12<<20)
	if _, err = rand.New(rand.NewSource(73)).Read(data); err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), data...)
	changed[len(changed)/2] ^= 0xff
	prepare := func(owner string, v *vault.Vault, entry string, content []byte) Reference {
		t.Helper()
		p, e := store.Prepare(ctx, owner, v, entry, 1, bytes.NewReader(content), int64(len(content)))
		if e != nil {
			t.Fatal(e)
		}
		if p.Reference.Version != chunkFormatVersion {
			t.Fatalf("write version=%d want %d", p.Reference.Version, chunkFormatVersion)
		}
		if e = store.MarkPublished(ctx, p.Operation); e == nil {
			e = store.Commit(ctx, p.Operation)
		}
		if e != nil {
			t.Fatal(e)
		}
		return p.Reference
	}
	first := prepare("alice", alice, "before", data)
	second := prepare("bob", bob, "after", changed)
	firstChunks := dependencySet(t, store, first.ObjectID)
	secondChunks := dependencySet(t, store, second.ObjectID)
	shared := 0
	for id := range firstChunks {
		if secondChunks[id] {
			shared++
		}
	}
	if shared < 4 || shared*2 < len(firstChunks) {
		t.Fatalf("one-byte edit failed to reuse stable chunks: first=%d second=%d shared=%d", len(firstChunks), len(secondChunks), shared)
	}
	var got bytes.Buffer
	if err = store.Read(ctx, "alice", alice, first, &got); err != nil || !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("chunked full read mismatch: %v", err)
	}
	// Read across a real CDC boundary, not an assumed fixed-size offset.
	boundary := firstChunkBoundary(t, store, alice, first)
	start := boundary - 17
	ranged := &captureRange{start: start, length: 41}
	if err = store.Read(ctx, "alice", alice, first, ranged); err != nil || !bytes.Equal(ranged.data.Bytes(), data[start:start+41]) {
		t.Fatalf("cross-chunk read mismatch at %d: %v", boundary, err)
	}
	stats, err := store.Metrics(ctx)
	if err != nil || stats.LogicalBytes != int64(len(data)*2) || stats.UniqueBytes >= stats.LogicalBytes || stats.Objects != int64(len(firstChunks)+len(secondChunks)-shared) {
		t.Fatalf("chunk metrics=%+v err=%v shared=%d", stats, err, shared)
	}
	if err = store.Release(ctx, "alice", first.EntryID, first.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	got.Reset()
	if err = store.Read(ctx, "bob", bob, second, &got); err != nil || !bytes.Equal(got.Bytes(), changed) {
		t.Fatalf("collecting old manifest damaged the remaining owner's chunks: %v", err)
	}
}

func dependencySet(t *testing.T, store *Store, parent string) map[string]bool {
	t.Helper()
	rows, err := store.db.Query(`SELECT child_id FROM object_dependencies WHERE parent_id=? ORDER BY child_id`, parent)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func firstChunkBoundary(t *testing.T, store *Store, v *vault.Vault, ref Reference) int64 {
	t.Helper()
	var wrapper []byte
	if err := store.db.QueryRow(`SELECT wrapped_key FROM owners WHERE owner_id=? AND entry_id=? AND revision=?`, store.keys.ownerToken("alice"), ref.EntryID, ref.Revision).Scan(&wrapper); err != nil {
		t.Fatal(err)
	}
	plain, err := v.Unwrap(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	defer cryptox.Zero(plain)
	var envelope struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(plain, &envelope); err != nil {
		t.Fatal(err)
	}
	key, err := cryptox.B64d(envelope.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer cryptox.Zero(key)
	file, err := os.Open(store.objectPath(ref.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var manifest bytes.Buffer
	if err = decryptFile(file, &manifest, ref.ObjectID, key); err != nil {
		t.Fatal(err)
	}
	var header, chunk manifestLine
	lines := bytes.Split(bytes.TrimSpace(manifest.Bytes()), []byte{'\n'})
	if len(lines) < 3 || json.Unmarshal(lines[0], &header) != nil || header.Type != "header" || json.Unmarshal(lines[1], &chunk) != nil || chunk.Type != "chunk" {
		t.Fatal("manifest is missing its authenticated header or first chunk")
	}
	return chunk.Length
}

type captureRange struct {
	start, length, seen int64
	data                bytes.Buffer
}

func (w *captureRange) Write(p []byte) (int, error) {
	lo, hi := w.seen, w.seen+int64(len(p))
	w.seen = hi
	start, end := w.start, w.start+w.length
	if hi <= start || lo >= end {
		return len(p), nil
	}
	from, to := max(start-lo, 0), min(end-lo, int64(len(p)))
	_, err := w.data.Write(p[from:to])
	return len(p), err
}
