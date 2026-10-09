package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

// Validate and back up photo transport records before removing their receipts.
// Ordinary file uploads, device credentials and account settings are preserved.
func resetUploadState(data, root, backup, owner string, v *vault.Vault, apply bool) error {
	entries, err := os.ReadDir(filepath.Join(root, ".weazl-photo-ingest"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".enc") {
			return errors.New("unexpected photo receipt entry")
		}
		raw, err := os.ReadFile(filepath.Join(root, ".weazl-photo-ingest", entry.Name()))
		if err != nil {
			return err
		}
		plain, err := v.Unwrap(raw)
		if err != nil {
			return err
		}
		var receipt struct {
			Uploads []string `json:"uploads"`
		}
		err = json.Unmarshal(plain, &receipt)
		clear(plain)
		if err != nil {
			return err
		}
		for _, id := range receipt.Uploads {
			if id == "" {
				continue
			}
			b, err := hex.DecodeString(id)
			if err != nil || len(b) != 16 {
				return errors.New("invalid photo upload ID")
			}
			ids[id] = true
		}
	}
	for id := range ids {
		for _, suffix := range []string{".json", ".part", ".chunk"} {
			name := id + suffix
			file := filepath.Join(data, "uploads", owner, name)
			info, err := os.Lstat(file)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("unexpected photo upload transport file")
			}
			if !apply {
				// Completed receipts normally have only a tiny manifest. Do not
				// buffer a large interrupted upload or remove it without a backup.
				if info.Size() > 16<<20 {
					return errors.New("large pending photo upload requires a transport backup first")
				}
				raw, err := os.ReadFile(file)
				if err != nil {
					return err
				}
				dir := filepath.Join(backup, "photo-transport")
				if err := os.MkdirAll(dir, 0700); err != nil {
					return err
				}
				if err := cryptox.AtomicWrite(filepath.Join(dir, name), raw, 0600); err != nil {
					return err
				}
			} else if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	if !apply {
		return nil
	}
	for _, name := range []string{
		".weazl-photo-ingest", ".weazl-mobile-parts", ".weazl-photo-components",
		".weazl-photo-albums.enc", ".weazl-photo-jobs.enc", ".weazl-photo-jobs.journal.enc",
		".weazl-photo-metadata.enc", ".weazl-photo-metadata-dry-run.enc", ".weazl-photo-preparation.enc",
		".weazl-photo-selections", ".weazl-photos-index.enc", ".weazl-live-photos.enc",
		".weazl-heic-scratch-recovery-v1.enc", ".weazl-preview-failures", ".weazl-previews",
	} {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return nil
}
