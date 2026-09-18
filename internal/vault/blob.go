package vault

import (
	"encoding/json"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type blobEnv struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func (v *Vault) Wrap(plain []byte) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.dek) == 0 {
		return nil, ErrLocked
	}
	nonce, ct, err := cryptox.Seal(v.dek, plain)
	if err != nil {
		return nil, err
	}
	return json.Marshal(blobEnv{Nonce: cryptox.B64(nonce), Ciphertext: cryptox.B64(ct)})
}

func (v *Vault) Unwrap(raw []byte) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.dek) == 0 {
		return nil, ErrLocked
	}
	var env blobEnv
	if json.Unmarshal(raw, &env) != nil {
		return nil, ErrPass
	}
	nonce, err := cryptox.B64d(env.Nonce)
	if err != nil {
		return nil, ErrPass
	}
	ct, err := cryptox.B64d(env.Ciphertext)
	if err != nil {
		return nil, ErrPass
	}
	return cryptox.Open(v.dek, nonce, ct)
}
