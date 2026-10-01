package library

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// convertHEIFPipe uses anonymous memory files so libheif can seek in the HEIF
// container. The temporary directory holds only suffix-preserving symlinks;
// source and decoded bytes never become regular filesystem files.
func convertHEIFPipe(parent context.Context, data []byte) ([]byte, error) {
	tool, err := exec.LookPath("heif-convert")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 35*time.Second)
	defer cancel()
	inFD, err := unix.MemfdCreate("weazl-heif-source", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	inFile := os.NewFile(uintptr(inFD), "heif-source")
	defer inFile.Close()
	if err := writeAll(inFile, data); err != nil {
		return nil, err
	}
	if _, err := inFile.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	outFD, err := unix.MemfdCreate("weazl-heif-preview", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, err
	}
	outFile := os.NewFile(uintptr(outFD), "heif-preview")
	defer outFile.Close()
	dir, err := os.MkdirTemp("", "weazl-heif-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	inPath, outPath := filepath.Join(dir, "source.heic"), filepath.Join(dir, "preview.jpg")
	if err := os.Symlink("/proc/self/fd/3", inPath); err != nil {
		return nil, err
	}
	if err := os.Symlink("/proc/self/fd/4", outPath); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, tool, "--quiet", inPath, outPath)
	cmd.ExtraFiles = []*os.File{inFile, outFile}
	cmd.WaitDelay = time.Second
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if _, err := outFile.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	output, err := io.ReadAll(io.LimitReader(outFile, thumbnailMaxInput+1))
	if err != nil {
		return nil, err
	}
	if len(output) == 0 || len(output) > thumbnailMaxInput {
		return nil, errors.New("HEIF derivative exceeds the in-memory size limit")
	}
	return output, nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
