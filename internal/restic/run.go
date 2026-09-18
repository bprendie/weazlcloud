package restic

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Repo struct {
	Location string
	Password []byte
}

type Runner struct {
	Binary string
	Stderr io.Writer
}

func New() Runner { return Runner{Binary: "restic"} }

func (r Runner) Run(ctx context.Context, repo Repo, stdin io.Reader, stdout io.Writer, args ...string) error {
	if len(repo.Password) == 0 {
		return errors.New("repository password is empty")
	}
	if repo.Location == "" {
		return errors.New("repository location is empty")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return err
	}
	defer pr.Close()
	cmdArgs := append([]string{"-r", repo.Location}, args...)
	cmd := exec.CommandContext(ctx, r.Binary, cmdArgs...)
	cmd.ExtraFiles = []*os.File{pr}
	cmd.Env = append(cleanEnv(), "RESTIC_PASSWORD_FILE=/proc/self/fd/3")
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	if stdout == nil {
		stdout = io.Discard
	}
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(&stderr, discard(r.Stderr))
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	_, werr := pw.Write(append([]byte(hex.EncodeToString(repo.Password)), '\n'))
	pw.Close()
	if werr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return werr
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if stdin == nil && strings.Contains(strings.ToLower(msg), "unable to create lock") {
			if uerr := r.Run(ctx, repo, nil, io.Discard, "unlock"); uerr == nil {
				return r.Run(ctx, repo, nil, stdout, args...)
			}
		}
		if len(msg) > 240 {
			msg = msg[:240]
		}
		return fmt.Errorf("library store: %s", msg)
	}
	return nil
}

func cleanEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "RESTIC_PASSWORD") {
			continue
		}
		out = append(out, e)
	}
	return out
}

func discard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
