package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectsPassphraseEnv(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "nug")
	if _, err := Load(); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestPublicBaseMustBeHTTPS(t *testing.T) {
	t.Setenv("WEAZLCLOUD_PUBLIC_BASE", "http://grab.example")
	if _, err := Load(); err == nil {
		t.Fatal("expected refusal")
	}
}

func TestLoadDefaults(t *testing.T) {
	for _, name := range forbiddenEnv {
		t.Setenv(name, "")
	}
	t.Setenv("WEAZLCLOUD_DATA", t.TempDir())
	t.Setenv("WEAZLCLOUD_PUBLIC_BASE", "")
	t.Setenv("WEAZLCLOUD_DRIVE_BASE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeskAddr != "127.0.0.1:7272" {
		t.Fatalf("desk %s", cfg.DeskAddr)
	}
}

func TestEnsureDataMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "node")
	cfg := Config{DataDir: dir, DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0"}
	if err := cfg.EnsureData(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("perm %o", st.Mode().Perm())
	}
}

func TestDriveBaseDAV(t *testing.T) {
	cfg := Config{
		DataDir: t.TempDir(), DeskAddr: "127.0.0.1:0", ShareAddr: "127.0.0.1:0", DriveAddr: "127.0.0.1:0",
		DriveBase: "davs://drive.example",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
