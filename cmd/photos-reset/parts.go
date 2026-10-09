package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/vault"
)

// Photo and Files uploads share this directory. Retire only authenticated photo
// sessions, after checking their encrypted headers against the rollback copy.
func resetPhotoParts(root, backup, owner string, v *vault.Vault, apply bool) error {
	base := filepath.Join(root, ".weazl-mobile-parts")
	entries, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var ids []string
	for _, entry := range entries {
		name := entry.Name()
		if name == ".live" || name == ".queue" || name == ".indexed-v1" {
			continue
		}
		id, err := hex.DecodeString(name)
		if err != nil || len(id) != 16 || !entry.IsDir() {
			return errors.New("unexpected mobile parts entry")
		}
		header := filepath.Join(base, name, "session.enc")
		info, err := os.Lstat(header)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 256<<10 {
			return errors.New("invalid mobile session header")
		}
		raw, err := os.ReadFile(header)
		if err != nil {
			return err
		}
		plain, err := v.Unwrap(raw)
		if err != nil {
			return err
		}
		var s struct {
			ID      string `json:"id"`
			OwnerID string `json:"owner_id"`
			Spec    struct {
				Kind string `json:"kind"`
			} `json:"spec"`
		}
		err = json.Unmarshal(plain, &s)
		clear(plain)
		if err != nil || s.ID != name || s.OwnerID != owner || (s.Spec.Kind != "photo" && s.Spec.Kind != "file") {
			return errors.New("mobile session identity mismatch")
		}
		if s.Spec.Kind != "photo" {
			continue
		}
		saved, err := os.ReadFile(filepath.Join(backup, ".weazl-mobile-parts", name, "session.enc"))
		if err != nil || digest(saved) != digest(raw) {
			return errors.New("mobile photo rollback header mismatch")
		}
		ids = append(ids, name)
	}
	if !apply {
		return nil
	}
	for _, id := range ids {
		if err := os.RemoveAll(filepath.Join(base, id)); err != nil {
			return err
		}
		for _, index := range []string{".live", ".queue"} {
			if err := os.Remove(filepath.Join(base, index, id)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
