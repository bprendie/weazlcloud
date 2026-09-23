package sharedstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type nodeKeys struct{ fingerprint, wrapping []byte }

func loadKeys(dir string) (nodeKeys, error) {
	path := filepath.Join(dir, "node.keys")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		b, err = cryptox.Random(64)
		if err != nil {
			return nodeKeys{}, err
		}
		if err = cryptox.AtomicWrite(path, b, 0o600); err != nil {
			cryptox.Zero(b)
			return nodeKeys{}, err
		}
	} else if err != nil {
		return nodeKeys{}, err
	}
	if len(b) != 64 {
		cryptox.Zero(b)
		return nodeKeys{}, errors.New("invalid shared-store key file")
	}
	if err = os.Chmod(path, 0o600); err != nil {
		cryptox.Zero(b)
		return nodeKeys{}, err
	}
	return nodeKeys{append([]byte(nil), b[:32]...), append([]byte(nil), b[32:]...)}, nil
}

func (k nodeKeys) fingerprintFor(length int64, sum []byte) []byte {
	h := hmac.New(sha256.New, k.fingerprint)
	h.Write([]byte("weazlcloud/shared-object/v1\x00"))
	h.Write([]byte{byte(length >> 56), byte(length >> 48), byte(length >> 40), byte(length >> 32), byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)})
	h.Write(sum)
	return h.Sum(nil)
}

func (k nodeKeys) chunkFingerprint(length int64, sum []byte) []byte {
	h := hmac.New(sha256.New, k.fingerprint)
	h.Write([]byte("weazlcloud/shared-chunk/rabin-v1\x00"))
	h.Write([]byte(chunkSettings))
	h.Write([]byte{byte(length >> 56), byte(length >> 48), byte(length >> 40), byte(length >> 32), byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)})
	h.Write(sum)
	return h.Sum(nil)
}

func (k nodeKeys) ownerToken(owner string) []byte {
	h := hmac.New(sha256.New, k.fingerprint)
	h.Write([]byte("weazlcloud/shared-owner/v1\x00"))
	h.Write([]byte(owner))
	return h.Sum(nil)
}

func opaqueID(raw []byte) string { return hex.EncodeToString(raw) }
