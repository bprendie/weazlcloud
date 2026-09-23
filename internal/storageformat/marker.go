package storageformat

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/bprendie/weazlcloud/internal/cryptox"
)

const (
	CurrentVersion uint16 = 1
	LegacyMode            = "restic-legacy"
	MarkerName            = ".weazl-storage.json"
)

var ErrUnsupported = errors.New("data directory uses an unsupported storage format")

type Marker struct {
	FormatVersion uint16 `json:"format_version"`
	MinimumReader uint16 `json:"minimum_reader_version"`
	MinimumWriter uint16 `json:"minimum_writer_version"`
	Mode          string `json:"mode"`
}

func Check(dataDir string) error {
	marker, err := read(dataDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if marker.FormatVersion != CurrentVersion || marker.MinimumReader > CurrentVersion || marker.MinimumWriter > CurrentVersion || marker.Mode != LegacyMode {
		return ErrUnsupported
	}
	return nil
}

func Initialize(dataDir string) error {
	if err := Check(dataDir); err != nil {
		return err
	}
	path := filepath.Join(dataDir, MarkerName)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	marker := Marker{FormatVersion: CurrentVersion, MinimumReader: CurrentVersion, MinimumWriter: CurrentVersion, Mode: LegacyMode}
	body, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	if err := cryptox.AtomicWrite(path, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return Check(dataDir)
}

func read(dataDir string) (Marker, error) {
	var marker Marker
	body, err := os.ReadFile(filepath.Join(dataDir, MarkerName))
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(body, &marker); err != nil {
		return Marker{}, err
	}
	if marker.FormatVersion == 0 || marker.MinimumReader == 0 || marker.MinimumWriter == 0 || marker.Mode == "" {
		return Marker{}, ErrUnsupported
	}
	return marker, nil
}
