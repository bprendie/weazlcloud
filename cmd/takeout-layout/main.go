// takeout-layout is an offline, owner-passphrase-authorized catalog migration.
// Stop the application and import scheduler before invoking it. Passwords are
// accepted only as JSON on stdin, never as command-line flags or environment.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/takeout"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	dir := flag.String("user-dir", "", "owner's vault directory")
	apply := flag.Bool("apply", false, "apply the checked mapping")
	flag.Parse()
	if *dir == "" {
		return errors.New("user-dir required; application must be stopped")
	}
	var input struct {
		Passphrase string `json:"passphrase"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 8192)).Decode(&input); err != nil {
		return errors.New("owner passphrase JSON required on stdin")
	}
	v := vault.New(filepath.Join(*dir, "vault.json"), filepath.Join(*dir, "node.key"))
	if err := v.Unlock([]byte(input.Passphrase)); err != nil {
		return err
	}
	defer v.Lock()
	location := filepath.Join(*dir, "catalog.enc")
	c := catalog.New(location, v)
	if err := c.LoadReadOnly(); err != nil {
		return err
	}
	before := c.All()
	mapping := make(map[string]string)
	files := 0
	var bytes int64
	for _, f := range before {
		if f.Reference != nil && f.Reference.Backend != catalog.ResticBackend {
			return errors.New("offline migration requires Restic references")
		}
		p, err := takeout.LayoutPath(f.Path)
		if err != nil {
			return err
		}
		if p != f.Path {
			mapping[f.Path] = p
		}
		if f.Present && !f.Folder {
			files++
			bytes += f.Size
		}
	}
	if err := c.Relocate(mapping, false); err != nil {
		return err
	}
	report := map[string]any{"files": files, "logical_bytes": bytes, "changed_paths": len(mapping), "applied": false, "layout": "root-v2"}
	if *apply {
		if err := backup(location); err != nil {
			return err
		}
		if err := migrateAlbums(*dir, v); err != nil {
			return err
		}
		if len(mapping) > 0 {
			if err := c.Relocate(mapping, true); err != nil {
				return err
			}
		}
		if err := c.LoadReadOnly(); err != nil {
			return err
		}
		if err := verifyFiles(before, c.All(), mapping); err != nil {
			return err
		}
		report["applied"] = true
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}

func backup(p string) error {
	in, err := os.Open(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := os.Stat(p + ".pre-root-v2"); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(p), ".layout-backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Link(out.Name(), p+".pre-root-v2"); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(p))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func migrateAlbums(dir string, v *vault.Vault) error {
	p := filepath.Join(dir, ".weazl-photo-albums.enc")
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := v.Unwrap(raw)
	if err != nil {
		return err
	}
	defer cryptox.Zero(plain)
	var old map[string]json.RawMessage
	if err = json.Unmarshal(plain, &old); err != nil {
		return err
	}
	next := make(map[string]json.RawMessage)
	for key, value := range old {
		i := strings.LastIndex(key, ":")
		if i < 0 {
			return errors.New("invalid album cache key")
		}
		name, err := takeout.LayoutPath(key[:i])
		if err != nil {
			return err
		}
		target := name + key[i:]
		if _, exists := next[target]; exists {
			return errors.New("album cache migration collision")
		}
		next[target] = value
	}
	if err = backup(p); err != nil {
		return err
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return err
	}
	defer cryptox.Zero(encoded)
	wrapped, err := v.Wrap(encoded)
	if err != nil {
		return err
	}
	return cryptox.AtomicWrite(p, wrapped, 0600)
}

func verifyFiles(before, after []catalog.File, mapping map[string]string) error {
	current := make(map[string]catalog.File)
	for _, f := range after {
		if !f.Folder {
			current[f.EntryID] = f
		}
	}
	count := 0
	for _, f := range before {
		if f.Folder {
			continue
		}
		count++
		expected := f
		if name, ok := mapping[f.Path]; ok && name != f.Path {
			expected.Path = name
			expected.Revision++
		}
		got, ok := current[f.EntryID]
		if !ok || !reflect.DeepEqual(expected, got) {
			return fmt.Errorf("content metadata changed for entry %s", f.EntryID)
		}
	}
	if count != len(current) {
		return errors.New("file count changed during migration")
	}
	return nil
}
