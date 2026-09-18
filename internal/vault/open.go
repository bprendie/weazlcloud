package vault

import (
	"encoding/json"
	"os"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (v *Vault) Forge(passphrase, confirm []byte) error {
	if len(passphrase) == 0 {
		return ErrEmpty
	}
	if string(passphrase) != string(confirm) {
		return ErrMismatch
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := os.Stat(v.path); err == nil {
		return ErrExists
	}
	dek, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return err
	}
	restic, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return err
	}
	drive, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return err
	}
	node, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return err
	}
	if err := v.write(passphrase, dek, restic, drive, node); err != nil {
		cryptox.Zero(dek)
		cryptox.Zero(restic)
		cryptox.Zero(drive)
		cryptox.Zero(node)
		return err
	}
	v.dek, v.resticPassword, v.driveToken = dek, restic, drive
	cryptox.Zero(node)
	return nil
}

func (v *Vault) Check(passphrase []byte) error {
	if len(passphrase) == 0 {
		return ErrEmpty
	}
	env, err := readEnvelope(v.path)
	if err != nil {
		return err
	}
	salt, err := cryptox.B64d(env.Salt)
	if err != nil || len(salt) != cryptox.SaltBytes {
		return ErrPass
	}
	key := cryptox.Derive(passphrase, salt)
	defer cryptox.Zero(key)
	dek, err := unwrap(key, env.PassNonce, env.PassWrap)
	if err != nil {
		return ErrPass
	}
	cryptox.Zero(dek)
	return nil
}

func (v *Vault) Unlock(passphrase []byte) error {
	if len(passphrase) == 0 {
		return ErrEmpty
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	env, err := readEnvelope(v.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrMissing
		}
		return err
	}
	salt, err := cryptox.B64d(env.Salt)
	if err != nil || len(salt) != cryptox.SaltBytes {
		return ErrPass
	}
	key := cryptox.Derive(passphrase, salt)
	defer cryptox.Zero(key)
	dek, err := unwrap(key, env.PassNonce, env.PassWrap)
	if err != nil {
		return ErrPass
	}
	return v.load(env, dek)
}

func (v *Vault) UnlockNode() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	node, err := os.ReadFile(v.nodePath)
	if err != nil {
		return err
	}
	defer cryptox.Zero(node)
	env, err := readEnvelope(v.path)
	if err != nil {
		return err
	}
	dek, err := unwrap(node, env.NodeNonce, env.NodeWrap)
	if err != nil {
		return ErrPass
	}
	return v.load(env, dek)
}

func (v *Vault) load(env envelope, dek []byte) error {
	plain, err := unwrap(dek, env.DataNonce, env.Ciphertext)
	if err != nil {
		cryptox.Zero(dek)
		return ErrPass
	}
	defer cryptox.Zero(plain)
	var data payload
	if json.Unmarshal(plain, &data) != nil || data.Check != checkPhrase {
		cryptox.Zero(dek)
		return ErrPass
	}
	restic, err := cryptox.B64d(data.ResticPassword)
	if err != nil {
		cryptox.Zero(dek)
		return ErrPass
	}
	drive, err := cryptox.B64d(data.DriveToken)
	if err != nil {
		cryptox.Zero(dek)
		cryptox.Zero(restic)
		return ErrPass
	}
	v.dek = dek
	v.resticPassword = restic
	v.driveToken = drive
	return nil
}

func unwrap(key []byte, nonceB64, ctB64 string) ([]byte, error) {
	nonce, err := cryptox.B64d(nonceB64)
	if err != nil {
		return nil, err
	}
	ct, err := cryptox.B64d(ctB64)
	if err != nil {
		return nil, err
	}
	return cryptox.Open(key, nonce, ct)
}

func readEnvelope(path string) (envelope, error) {
	var env envelope
	b, err := os.ReadFile(path)
	if err != nil {
		return env, err
	}
	if json.Unmarshal(b, &env) != nil || env.Version != format {
		return env, ErrPass
	}
	return env, nil
}
