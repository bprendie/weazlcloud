package cryptox

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestStreamFileRandomAccessAndTamperDetection(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	plain := bytes.Repeat([]byte("file-content"), 200000)
	var cipher bytes.Buffer
	w, err := NewStreamFileWriter(&cipher, key)
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < len(plain); offset += 7000 {
		if _, err := w.Write(plain[offset:min(offset+7000, len(plain))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if w.Size() != int64(len(plain)) || bytes.Contains(cipher.Bytes(), []byte("file-content")) {
		t.Fatal("cipher file size/plaintext failure")
	}
	path := filepath.Join(t.TempDir(), "archive.wza")
	if err := os.WriteFile(path, cipher.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r, err := OpenStreamFile(ctx, path, key, w.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, offset := range []int64{0, streamFileChunk - 3, streamFileChunk + 41, int64(len(plain) - 20)} {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, 20)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body, plain[offset:offset+20]) {
			t.Fatal("range decrypted incorrect bytes")
		}
	}
	cancel()
	if _, err := r.Read(make([]byte, 1)); err == nil {
		t.Fatal("cancelled reader continued")
	}
	corrupt := append([]byte(nil), cipher.Bytes()...)
	corrupt[4+12] ^= 1
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	bad, err := OpenStreamFile(context.Background(), path, key, w.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if _, err := bad.Read(make([]byte, 20)); err == nil {
		t.Fatal("tampered chunk authenticated")
	}
	if _, err := OpenStreamFile(context.Background(), path, key, w.Size()-1); err == nil {
		t.Fatal("tampered length accepted")
	}
	// Reordering full authenticated frames cannot retain their position binding.
	copy(corrupt[4:4+streamFileChunk+streamFileOverhead], cipher.Bytes()[4+streamFileChunk+streamFileOverhead:4+2*(streamFileChunk+streamFileOverhead)])
	if err := os.WriteFile(path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	swapped, err := OpenStreamFile(context.Background(), path, key, w.Size())
	if err != nil {
		t.Fatal(err)
	}
	defer swapped.Close()
	if _, err := swapped.Read(make([]byte, 20)); err == nil {
		t.Fatal("reordered chunk authenticated")
	}
}
