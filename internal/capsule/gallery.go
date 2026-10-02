package capsule

import (
	"archive/zip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

type GalleryItem struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id,omitempty"`
	MotionID    string `json:"motion_id,omitempty"`
	MotionType  string `json:"motion_type,omitempty"`
	Name        string `json:"name"`
	MediaType   string `json:"media_type"`
	PreviewType string `json:"preview_type,omitempty"`
	Size        int64  `json:"size"`
	Revision    uint64 `json:"revision"`
}

type GalleryManifest struct {
	Title            string        `json:"title"`
	Items            []GalleryItem `json:"items"`
	OriginalMetadata bool          `json:"original_metadata"`
}

type GallerySource struct {
	SourceID, ParentID string
	MotionPreview      func() ([]byte, error)
	Item               GalleryItem
	Original           StreamSource
	Preview            func() ([]byte, string, error)
}

// MintGallery writes a frozen, encrypted selection and its ZIP. No private
// vault keys or library paths become part of the guest-facing manifest.
func (s *Store) MintGallery(rec Record, phrase string, sources []GallerySource) (Record, error) {
	if s.mobileOrigin != nil {
		return s.mobileOrigin.mintGallery(rec, phrase, sources, s.mobileID, s.mobilePublish)
	}
	return s.mintGallery(rec, phrase, sources, "", nil)
}

func (s *Store) mintGallery(rec Record, phrase string, sources []GallerySource, identity string, publish MobilePublicationGuard) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(sources) == 0 || len(sources) > 10_000 || rec.Limit < 1 || rec.Limit > 10000 {
		return Record{}, ErrGone
	}
	raw, err := cryptox.Random(16)
	if err != nil {
		return Record{}, err
	}
	rec.ID, rec.Kind, rec.Files = encodeToken(raw), "gallery", nil
	if identity != "" {
		if err := s.prepareMobileDirectory(identity); err != nil {
			return Record{}, err
		}
		rec.ID = identity
	}
	key, err := cryptox.Random(cryptox.KeyBytes)
	if err != nil {
		return Record{}, err
	}
	defer cryptox.Zero(key)
	dir := filepath.Join(s.root, rec.ID)
	if err := os.MkdirAll(filepath.Join(dir, "gallery"), 0700); err != nil {
		return Record{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(dir)
		}
	}()
	manifest := GalleryManifest{Title: rec.Label, Items: []GalleryItem{}, OriginalMetadata: true}
	for _, source := range sources {
		if source.Original == nil || source.Item.Size < 0 {
			return Record{}, ErrGone
		}
		raw, err := cryptox.Random(16)
		if err != nil {
			return Record{}, err
		}
		item := source.Item
		item.ID, item.Name = encodeToken(raw), galleryName(item.Name)
		originalKey := galleryKey(key, item.ID+".original")
		count, err := writeGalleryStream(filepath.Join(dir, "gallery", item.ID+".original"), originalKey, source.Original)
		cryptox.Zero(originalKey)
		if err != nil {
			return Record{}, err
		}
		if count != item.Size {
			return Record{}, errors.New("gallery original size changed")
		}
		if source.Preview != nil {
			body, kind, err := source.Preview()
			if err != nil {
				return Record{}, err
			}
			if len(body) > 8<<20 || len(body) != 0 && kind != "image/jpeg" && kind != "image/png" {
				clear(body)
				return Record{}, errors.New("gallery preview is invalid")
			}
			previewKey := galleryKey(key, item.ID+".preview")
			_, err = writeGalleryStream(filepath.Join(dir, "gallery", item.ID+".preview"), previewKey, func(w io.Writer) error { _, e := w.Write(body); return e })
			cryptox.Zero(previewKey)
			clear(body)
			if err != nil {
				return Record{}, err
			}
			item.PreviewType = kind
		}
		if source.MotionPreview != nil && source.ParentID != "" {
			body, e := source.MotionPreview()
			if e == nil && len(body) > 0 && len(body) <= 8<<20 {
				motionKey := galleryKey(key, item.ID+".motion")
				_, e = writeGalleryStream(filepath.Join(dir, "gallery", item.ID+".motion"), motionKey, func(w io.Writer) error { _, err := w.Write(body); return err })
				clear(motionKey)
				clear(body)
				if e != nil {
					return Record{}, e
				}
				item.MotionType = "video/mp4"
			} else {
				clear(body)
			}
		}
		manifest.Items = append(manifest.Items, item)
	}
	galleryIDs := map[string]string{}
	for i, source := range sources {
		if source.SourceID != "" {
			galleryIDs[source.SourceID] = manifest.Items[i].ID
		}
	}
	for i, source := range sources {
		if parent := galleryIDs[source.ParentID]; parent != "" {
			manifest.Items[i].ParentID = parent
			for j := range manifest.Items {
				if manifest.Items[j].ID == parent && manifest.Items[i].MotionType != "" {
					manifest.Items[j].MotionID = manifest.Items[i].ID
				}
			}
		}
	}
	plain, err := json.Marshal(manifest)
	if err != nil {
		return Record{}, err
	}
	defer clear(plain)
	manifestKey := galleryKey(key, "manifest")
	_, err = writeGalleryStream(filepath.Join(dir, "gallery", "manifest"), manifestKey, func(w io.Writer) error { _, e := w.Write(plain); return e })
	cryptox.Zero(manifestKey)
	if err != nil {
		return Record{}, err
	}
	rec.Size, err = writeGalleryStream(filepath.Join(dir, "payload"), key, func(w io.Writer) error {
		z := zip.NewWriter(w)
		for index, item := range manifest.Items {
			entry, e := z.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("%04d/%s", index+1, item.Name), Method: zip.Store})
			if e != nil {
				return e
			}
			partKey := galleryKey(key, item.ID+".original")
			e = readGalleryStream(filepath.Join(dir, "gallery", item.ID+".original"), partKey, entry)
			cryptox.Zero(partKey)
			if e != nil {
				return e
			}
		}
		return z.Close()
	})
	if err != nil {
		return Record{}, err
	}
	if rec.Gate == "passphrase" {
		if phrase == "" {
			return Record{}, ErrPhrase
		}
		err = wrapKey(dir, key, []byte(phrase))
	} else {
		rec.Gate = "open"
		err = cryptox.AtomicWrite(filepath.Join(dir, "open.key"), key, 0600)
	}
	if err != nil {
		return Record{}, err
	}
	if err := writeMobileMeta(dir, rec, publish); err != nil {
		return Record{}, err
	}
	committed = true
	return rec, nil
}

func galleryKey(key []byte, part string) []byte {
	hash := hmac.New(sha256.New, key)
	_, _ = io.WriteString(hash, "weazl-gallery-v1:"+part)
	return hash.Sum(nil)
}

func writeGalleryStream(name string, key []byte, source StreamSource) (int64, error) {
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	count, err := encryptStream(f, key, source)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return count, err
}

func readGalleryStream(name string, key []byte, dst io.Writer) error {
	f, err := os.Open(name)
	if err != nil {
		return ErrGone
	}
	defer f.Close()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != streamMagic {
		return ErrGone
	}
	return decryptStream(f, key, dst)
}

func galleryName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(name)
	if name == "" || name == "." || name == ".." {
		return "photo"
	}
	return name
}
