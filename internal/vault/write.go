package vault

import (
	"encoding/json"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

func (v *Vault) write(passphrase, dek, restic, drive, node []byte) error {
	salt, err := cryptox.Random(cryptox.SaltBytes)
	if err != nil {
		return err
	}
	passKey := cryptox.Derive(passphrase, salt)
	defer cryptox.Zero(passKey)
	passNonce, passWrap, err := cryptox.Seal(passKey, dek)
	if err != nil {
		return err
	}
	nodeNonce, nodeWrap, err := cryptox.Seal(node, dek)
	if err != nil {
		return err
	}
	plain, err := json.Marshal(payload{
		Check:          checkPhrase,
		ResticPassword: cryptox.B64(restic),
		DriveToken:     cryptox.B64(drive),
	})
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	dataNonce, ct, err := cryptox.Seal(dek, plain)
	if err != nil {
		return err
	}
	env := envelope{
		Version:    format,
		Salt:       cryptox.B64(salt),
		PassNonce:  cryptox.B64(passNonce),
		PassWrap:   cryptox.B64(passWrap),
		NodeNonce:  cryptox.B64(nodeNonce),
		NodeWrap:   cryptox.B64(nodeWrap),
		DataNonce:  cryptox.B64(dataNonce),
		Ciphertext: cryptox.B64(ct),
	}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(v.path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return cryptox.AtomicWrite(v.nodePath, node, 0o600)
}
