package recovery

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bprendie/weazlcloud/internal/vault"
)

func TestKitRoundTrip(t *testing.T) {
	dir := t.TempDir()
	vp := filepath.Join(dir, "vault.json")
	v := vault.New(vp, filepath.Join(dir, "node.key"))
	pass := []byte("kit-pass")
	if err := v.Forge(pass, pass); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "weazlcloud-recovery.wzck")
	cfg := []byte(`{"grab":"https://grab.example"}`)
	if err := Export(out, vp, cfg, pass); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, pass) || bytes.Contains(raw, cfg) {
		t.Fatal("canary in kit")
	}
	man, err := Verify(out, pass)
	if err != nil {
		t.Fatal(err)
	}
	if man.Warning == "" || man.Files["vault.enc"] == "" {
		t.Fatalf("manifest %+v", man)
	}
	if _, err := Verify(out, []byte("nope")); err == nil {
		t.Fatal("wrong phrase opened kit")
	}
}
