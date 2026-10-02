package mobileparts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentIdenticalAppendAndOwnerIsolation(t *testing.T) {
	m, res, q := fixture(t)
	data := []byte("simultaneous-mobile-part")
	create(t, m, res, data)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), bytes.NewReader(data))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := m.Status(res, sessionID, device)
	if err != nil || current.Components[0].ReceivedParts != 1 || current.Components[0].ReceivedBytes != int64(len(data)) {
		t.Fatalf("concurrent duplicate counters: %+v %v", current, err)
	}
	expected := reserved(t, q)
	otherOwner, otherID := strings.Repeat("d", 32), strings.Repeat("e", 32)
	if _, err = m.Create(res, otherOwner, otherID, specFor(data)); err != nil {
		t.Fatal(err)
	}
	defer m.ReleaseOwner(otherOwner)
	combined := reserved(t, q)
	if err = m.ReleaseOwner(owner); err != nil {
		t.Fatal(err)
	}
	if reserved(t, q) != combined-expected {
		t.Fatal("owner cleanup released another owner's quota")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.gates) != 0 || len(m.checked) != 0 {
		t.Fatal("concurrent calls retained idle gates")
	}
}

func TestAppendIncomingFileIsEncryptedWhileStreaming(t *testing.T) {
	m, res, _ := fixture(t)
	data := bytes.Repeat([]byte("private-live-stream-marker"), 100000)
	create(t, m, res, data)
	inspected := false
	input := &inspectReader{Reader: bytes.NewReader(data), inspect: func() error {
		entries, err := os.ReadDir(keyFor(res, sessionID))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".incoming-") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(keyFor(res, sessionID), entry.Name()))
			if err != nil {
				return err
			}
			if len(body) < 1<<20 {
				continue
			}
			if !bytes.HasPrefix(body, []byte("WZA1")) || bytes.Contains(body, []byte("private-live-stream-marker")) {
				return errors.New("plaintext in live incoming file")
			}
			inspected = true
		}
		return nil
	}}
	_, err := m.Append(context.Background(), res, sessionID, device, "original", 0, int64(len(data)), digest(data), input)
	if err != nil || !inspected {
		t.Fatalf("live confidentiality: inspected=%v err=%v", inspected, err)
	}
}

type inspectReader struct {
	Reader  io.Reader
	inspect func() error
}

func (r *inspectReader) Read(p []byte) (int, error) {
	if err := r.inspect(); err != nil {
		return 0, fmt.Errorf("inspect live stage: %w", err)
	}
	return r.Reader.Read(p)
}
