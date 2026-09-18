package recovery

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

const (
	format     = "weazlcloud-recovery"
	schema     = 1
	warning    = "NO RECOVERY: the vault passphrase is required and cannot be reset."
	maxArchive = 64 << 20
)

type envelope struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	CreatedAt     time.Time         `json:"created_at"`
	Warning       string            `json:"warning"`
	Files         map[string]string `json:"files"`
}

func Export(out, vaultPath string, cfg []byte, passphrase []byte) error {
	if len(passphrase) == 0 {
		return errors.New("passphrase must not be empty")
	}
	vault, err := os.ReadFile(vaultPath)
	if err != nil {
		return err
	}
	plain, err := pack(vault, cfg)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	env, err := seal(plain, passphrase)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(out, append(raw, '\n'), 0o600)
}

func Verify(path string, passphrase []byte) (Manifest, error) {
	m, vault, cfg, err := Open(path, passphrase)
	cryptox.Zero(vault)
	cryptox.Zero(cfg)
	return m, err
}

func Open(path string, passphrase []byte) (Manifest, []byte, []byte, error) {
	var m Manifest
	if len(passphrase) == 0 {
		return m, nil, nil, errors.New("passphrase must not be empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return m, nil, nil, err
	}
	var env envelope
	if json.Unmarshal(b, &env) != nil || env.Format != format || env.Version != schema {
		return m, nil, nil, errors.New("invalid or unsupported recovery kit")
	}
	plain, err := unseal(env, passphrase)
	if err != nil {
		return m, nil, nil, errors.New("incorrect passphrase or damaged recovery kit")
	}
	m, files, err := unpack(plain)
	cryptox.Zero(plain)
	if err != nil {
		return m, nil, nil, err
	}
	return m, files["vault.enc"], files["config.json"], nil
}

func seal(plain, passphrase []byte) (envelope, error) {
	salt, err := cryptox.Random(cryptox.SaltBytes)
	if err != nil {
		return envelope{}, err
	}
	key := cryptox.Derive(passphrase, salt)
	defer cryptox.Zero(key)
	nonce, ct, err := cryptox.Seal(key, plain)
	if err != nil {
		return envelope{}, err
	}
	return envelope{
		Format: format, Version: schema,
		Salt: cryptox.B64(salt), Nonce: cryptox.B64(nonce), Ciphertext: cryptox.B64(ct),
	}, nil
}

func unseal(env envelope, passphrase []byte) ([]byte, error) {
	salt, err := cryptox.B64d(env.Salt)
	if err != nil || len(salt) != cryptox.SaltBytes {
		return nil, errors.New("invalid salt")
	}
	nonce, err := cryptox.B64d(env.Nonce)
	if err != nil {
		return nil, err
	}
	ct, err := cryptox.B64d(env.Ciphertext)
	if err != nil {
		return nil, err
	}
	key := cryptox.Derive(passphrase, salt)
	defer cryptox.Zero(key)
	return cryptox.Open(key, nonce, ct)
}

func pack(vault, cfg []byte) ([]byte, error) {
	files := map[string][]byte{"vault.enc": vault, "config.json": cfg}
	man := Manifest{SchemaVersion: schema, CreatedAt: time.Now().UTC(), Warning: warning, Files: map[string]string{}}
	for name, b := range files {
		sum := sha256.Sum256(b)
		man.Files[name] = hex.EncodeToString(sum[:])
	}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{"vault.enc", "config.json", "manifest.json"} {
		body := files[name]
		if name == "manifest.json" {
			body = append(mb, '\n')
		}
		w, err := z.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(body); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func unpack(data []byte) (Manifest, map[string][]byte, error) {
	var man Manifest
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return man, nil, err
	}
	content := map[string][]byte{}
	for _, f := range zr.File {
		if f.Name != "vault.enc" && f.Name != "config.json" && f.Name != "manifest.json" {
			return man, nil, errors.New("recovery kit contains an unexpected path")
		}
		r, err := f.Open()
		if err != nil {
			return man, nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, maxArchive))
		r.Close()
		if err != nil {
			return man, nil, err
		}
		content[f.Name] = b
	}
	if json.Unmarshal(content["manifest.json"], &man) != nil || man.SchemaVersion != schema {
		return man, nil, errors.New("recovery manifest missing or invalid")
	}
	for name, want := range man.Files {
		sum := sha256.Sum256(content[name])
		if hex.EncodeToString(sum[:]) != want {
			return man, nil, errors.New("recovery file failed checksum")
		}
	}
	if _, ok := content["vault.enc"]; !ok {
		return man, nil, errors.New("encrypted vault is missing")
	}
	return man, content, nil
}
