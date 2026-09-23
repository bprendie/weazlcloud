package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

var forbiddenEnv = []string{
	"RESTIC_PASSWORD",
	"RESTIC_PASSWORD_FILE",
	"WEAZLCLOUD_PASSPHRASE",
	"WEAZLCLOUD_PASSWORD",
	"WEAZLCLOUD_VAULT_PASSPHRASE",
}

type Config struct {
	DataDir          string
	DeskAddr         string
	ShareAddr        string
	DriveAddr        string
	PublicBase       string
	DriveBase        string
	SecureCookies    bool
	MaintenanceQuiet time.Duration
	StorageBackend   string
}

func Load() (Config, error) {
	if err := rejectSecrets(); err != nil {
		return Config{}, err
	}
	cfg := Config{
		DataDir:          env("WEAZLCLOUD_DATA", "data"),
		DeskAddr:         env("WEAZLCLOUD_DESK_ADDR", "127.0.0.1:7272"),
		ShareAddr:        env("WEAZLCLOUD_SHARE_ADDR", "127.0.0.1:7273"),
		DriveAddr:        env("WEAZLCLOUD_DRIVE_ADDR", "127.0.0.1:7274"),
		PublicBase:       strings.TrimSpace(os.Getenv("WEAZLCLOUD_PUBLIC_BASE")),
		DriveBase:        strings.TrimSpace(os.Getenv("WEAZLCLOUD_DRIVE_BASE")),
		SecureCookies:    strings.EqualFold(strings.TrimSpace(os.Getenv("WEAZLCLOUD_SECURE_COOKIES")), "true"),
		MaintenanceQuiet: 5 * time.Minute,
		StorageBackend:   env("WEAZLCLOUD_STORAGE_BACKEND", "restic"),
	}
	if raw := strings.TrimSpace(os.Getenv("WEAZLCLOUD_MAINTENANCE_QUIET")); raw != "" {
		quiet, err := time.ParseDuration(raw)
		if err != nil || quiet <= 0 {
			return Config{}, errors.New("maintenance quiet period must be a positive duration")
		}
		cfg.MaintenanceQuiet = quiet
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.DataDir) == "" {
		return errors.New("data directory is empty")
	}
	if c.StorageBackend != "" && c.StorageBackend != "restic" && c.StorageBackend != "shared-experimental" {
		return errors.New("storage backend must be restic or shared-experimental")
	}
	for _, addr := range []string{c.DeskAddr, c.ShareAddr, c.DriveAddr} {
		if _, err := net.ResolveTCPAddr("tcp", addr); err != nil {
			return fmt.Errorf("listen address %q: %w", addr, err)
		}
	}
	if c.PublicBase != "" {
		if err := checkBase(c.PublicBase, "https"); err != nil {
			return fmt.Errorf("public grab base: %w", err)
		}
	}
	if c.DriveBase != "" {
		if err := checkBase(c.DriveBase, "davs", "https"); err != nil {
			return fmt.Errorf("drive base: %w", err)
		}
	}
	return nil
}

func (c Config) EnsureData() error {
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(c.DataDir, 0o700)
}

func rejectSecrets() error {
	for _, name := range forbiddenEnv {
		if os.Getenv(name) != "" {
			return fmt.Errorf("%s must not be set; the passphrase never lives in the environment", name)
		}
	}
	return nil
}

func checkBase(raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	ok := false
	for _, s := range schemes {
		if u.Scheme == s {
			ok = true
			break
		}
	}
	if !ok || u.Host == "" || u.User != nil {
		return errors.New("need a host and an https/davs scheme, with no userinfo")
	}
	return nil
}

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
