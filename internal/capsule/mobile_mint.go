package capsule

import (
	"crypto/aes"
	"crypto/cipher"
	"io"
)

func (s *Store) MintStream(rec Record, phrase string, source StreamSource) (Record, error) {
	if s.mobileOrigin != nil {
		if s.mobilePrepare != nil {
			var release func()
			var err error
			rec, source, release, err = s.mobilePrepare(rec, source)
			if err != nil {
				return Record{}, err
			}
			defer release()
		}
		return s.mobileOrigin.mintStream(rec, phrase, source, s.mobileID, s.mobilePublish)
	}
	return s.mintStream(rec, phrase, source, "", nil)
}

func encryptStream(dst io.Writer, key []byte, source StreamSource) (int64, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return 0, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return 0, err
	}
	if _, err := io.WriteString(dst, streamMagic); err != nil {
		return 0, err
	}
	var count countingWriter
	count.w = &streamEncryptWriter{dst: dst, gcm: gcm}
	if err := source(&count); err != nil {
		return count.n, err
	}
	if err := count.w.(*streamEncryptWriter).finish(); err != nil {
		return count.n, err
	}
	return count.n, nil
}
