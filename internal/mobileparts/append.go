package mobileparts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/filesvc"
)

// AppendGuarded receives into a private encrypted temporary file, then publishes
// under a short session gate and optional current-authorization barrier.
func (m *Manager) AppendGuarded(ctx context.Context, res *filesvc.Resource, id, device, component string, index, length int64, hash string, src io.Reader, guard func(func() error) error) (out View, err error) {
	trace := receiveTrace{start: time.Now(), expected: length}
	defer func() { trace.finish(err) }()
	if err = ctx.Err(); err != nil {
		return
	}
	hash = strings.ToLower(hash)
	unlock, e := m.lockContext(ctx, res, id)
	if e != nil {
		return out, e
	}
	trace.admission = time.Since(trace.start)
	s, e := m.load(res, id)
	if e != nil {
		unlock()
		return out, e
	}
	c, e := receiveComponent(s, device, component, index, length, hash)
	if e != nil {
		unlock()
		return out, e
	}
	base := filepath.Join(keyFor(res, id), partName(component, index))
	var old partReceipt
	duplicate := readSealed(res, base+".receipt.enc", &old)
	if duplicate == nil && (old.Hash != hash || old.Size != length) {
		unlock()
		return out, ErrConflict
	}
	if duplicate != nil && !errors.Is(duplicate, ErrNotFound) {
		unlock()
		return out, duplicate
	}
	if duplicate != nil && s.Status != "uploading" {
		unlock()
		return out, ErrConflict
	}
	ctx, done, e := m.beginReceive(ctx, res, s, src)
	unlock()
	if e != nil {
		return out, e
	}
	defer done()
	reader := &receiveReader{ctx: ctx, src: src, trace: &trace}
	h := sha256.New()
	var name string
	if duplicate == nil {
		_, err = io.Copy(h, io.LimitReader(reader, length+1))
	} else {
		key, keyErr := partKey(s, c.ID, index)
		if keyErr != nil {
			return out, keyErr
		}
		defer clear(key)
		var f *os.File
		f, err = os.CreateTemp(keyFor(res, id), ".incoming-")
		if err != nil {
			return
		}
		name = f.Name()
		defer os.Remove(name)
		defer f.Close()
		if err = f.Chmod(0600); err != nil {
			return
		}
		var w io.WriteCloser
		w, err = cryptox.NewStreamFileWriter(f, key)
		if err != nil {
			return
		}
		_, err = io.Copy(w, io.TeeReader(io.LimitReader(reader, length+1), h))
		closeErr := w.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return
		}
		if err = validateReceived(ctx, trace.bytes, length, h.Sum(nil), hash); err != nil {
			return
		}
		start := time.Now()
		err = f.Sync()
		trace.sync = time.Since(start)
		if err != nil {
			return
		}
		if err = f.Close(); err != nil {
			return
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return
	}
	if err = validateReceived(ctx, trace.bytes, length, h.Sum(nil), hash); err != nil {
		return
	}
	start := time.Now()
	unlock, e = m.lockContext(ctx, res, id)
	if e != nil {
		return out, e
	}
	defer unlock()
	trace.commitWait = time.Since(start)
	publish := func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		latest, e := m.load(res, id)
		if e != nil {
			return e
		}
		if latest.Key != s.Key || !latest.CreatedAt.Equal(s.CreatedAt) || latest.Status == "cancelled" {
			return ErrConflict
		}
		if _, e = receiveComponent(latest, device, component, index, length, hash); e != nil {
			return e
		}
		var receipt partReceipt
		if e = readSealed(res, base+".receipt.enc", &receipt); e == nil {
			if receipt.Hash != hash || receipt.Size != length {
				return ErrConflict
			}
			out = view(latest)
			return nil
		} else if !errors.Is(e, ErrNotFound) {
			return e
		}
		if name == "" || latest.Status != "uploading" {
			return ErrConflict
		}
		if e = os.Rename(name, base+".wza"); e != nil {
			return e
		}
		if e = syncDir(keyFor(res, id)); e != nil {
			return e
		}
		if e = writeSealed(res, base+".receipt.enc", partReceipt{component, index, length, hash}); e != nil {
			return e
		}
		// Reconcile durable receipts rather than trusting stale admission counters.
		if e = m.reconcile(res, &latest); e != nil {
			return e
		}
		if e = m.save(res, &latest); e != nil {
			return e
		}
		out = view(latest)
		return nil
	}
	start = time.Now()
	if guard != nil {
		err = guard(publish)
	} else {
		err = publish()
	}
	trace.commit = time.Since(start)
	return
}
func receiveComponent(s Session, device, component string, index, length int64, hash string) (Component, error) {
	if s.Spec.DeviceID != device {
		return Component{}, ErrNotFound
	}
	for _, c := range s.Spec.Components {
		if c.ID == component {
			expected, e := partLength(c, index)
			if e != nil || expected != length || !validHash(hash) {
				return c, ErrInvalid
			}
			return c, nil
		}
	}
	return Component{}, ErrInvalid
}
func validateReceived(ctx context.Context, n, want int64, sum []byte, hash string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if n != want {
		return ErrInvalid
	}
	if hex.EncodeToString(sum) != hash {
		return ErrChecksum
	}
	return nil
}
