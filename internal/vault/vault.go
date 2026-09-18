package vault

import (
	"encoding/hex"
	"errors"
	"os"
	"sync"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

const (
	checkPhrase = "weazlcloud-vault"
	format      = 1
)

var (
	ErrLocked   = errors.New("vault is locked")
	ErrExists   = errors.New("vault already exists")
	ErrMissing  = errors.New("vault does not exist")
	ErrPass     = errors.New("incorrect passphrase or damaged vault")
	ErrEmpty    = errors.New("passphrase must not be empty")
	ErrMismatch = errors.New("passphrases do not match")
)

type envelope struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt"`
	PassNonce  string `json:"pass_nonce"`
	PassWrap   string `json:"pass_wrap"`
	NodeNonce  string `json:"node_nonce,omitempty"`
	NodeWrap   string `json:"node_wrap,omitempty"`
	DataNonce  string `json:"data_nonce"`
	Ciphertext string `json:"ciphertext"`
}

type payload struct {
	Check          string `json:"check"`
	ResticPassword string `json:"restic_password"`
	DriveToken     string `json:"drive_token"`
}

type Vault struct {
	mu             sync.Mutex
	path           string
	nodePath       string
	dek            []byte
	resticPassword []byte
	driveToken     []byte
}

func New(path, nodePath string) *Vault {
	return &Vault{path: path, nodePath: nodePath}
}

func Paths(dataDir string) (vaultPath, nodePath string) {
	return dataDir + "/vault.json", dataDir + "/node.key"
}

func (v *Vault) Path() string { return v.path }

func (v *Vault) Exists() bool {
	_, err := os.Stat(v.path)
	return err == nil
}

func (v *Vault) Unlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.dek) == cryptox.KeyBytes
}

func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	cryptox.Zero(v.dek)
	cryptox.Zero(v.resticPassword)
	cryptox.Zero(v.driveToken)
	v.dek, v.resticPassword, v.driveToken = nil, nil, nil
}

func (v *Vault) Secrets() (restic, drive []byte, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.dek) == 0 {
		return nil, nil, ErrLocked
	}
	return append([]byte(nil), v.resticPassword...), append([]byte(nil), v.driveToken...), nil
}

func (v *Vault) DriveToken() (string, error) {
	_, drive, err := v.Secrets()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(drive), nil
}
