// Offline synthetic catalog fixture. Never installed in the production image.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/cryptox"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "synthetic fixture rejected:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("root and count required")
	}
	root := os.Args[1]
	n, e := strconv.Atoi(os.Args[2])
	if e != nil || n != 24000 {
		return fmt.Errorf("count must be 24000")
	}
	if _, e = os.Stat(filepath.Join(root, ".weazl-throughput-fixture")); e != nil {
		return e
	}
	raw, e := os.ReadFile(filepath.Join(root, "users.json"))
	if e != nil {
		return e
	}
	var state struct {
		Users []struct{ ID, Username string }
	}
	if e = json.Unmarshal(raw, &state); e != nil {
		return e
	}
	if len(state.Users) != 1 || state.Users[0].Username != "throughput" {
		return fmt.Errorf("not a disposable throughput fixture")
	}
	owner := filepath.Join(root, "users", state.Users[0].ID)
	v := vault.New(filepath.Join(owner, "vault.json"), filepath.Join(owner, "node.key"))
	if e = v.UnlockNode(); e != nil {
		return e
	}
	defer v.Lock()
	path := filepath.Join(owner, "catalog.enc")
	raw, e = os.ReadFile(path)
	if e != nil {
		return e
	}
	plain, e := v.Unwrap(raw)
	if e != nil {
		return e
	}
	defer clear(plain)
	var doc map[string]json.RawMessage
	if e = json.Unmarshal(plain, &doc); e != nil {
		return e
	}
	var wire struct {
		Entries []catalog.File `json:"entries"`
	}
	if e = json.Unmarshal(doc["files"], &wire); e != nil {
		return e
	}
	rows := wire.Entries
	before := len(rows)
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	// Folders have no fabricated storage references. Long captions model the
	// metadata size of a populated catalog, without uploading thousands of files.
	for i := range n {
		path := fmt.Sprintf("synthetic-folder-%05d", i)
		h := sha256.Sum256([]byte(path))
		rows = append(rows, catalog.File{EntryID: hex.EncodeToString(h[:16]), Revision: 1, Path: path, Folder: true, Present: true, Mtime: at, Caption: strings.Repeat("synthetic fixture metadata ", 30)})
	}
	wire.Entries = rows
	doc["files"], e = json.Marshal(wire)
	if e != nil {
		return e
	}
	plain, e = json.Marshal(doc)
	if e != nil {
		return e
	}
	defer clear(plain)
	sealed, e := v.Wrap(plain)
	if e != nil {
		return e
	}
	if e = cryptox.AtomicWrite(path, sealed, 0600); e != nil {
		return e
	}
	c := catalog.New(path, v)
	if e = c.LoadReadOnly(); e != nil {
		return e
	}
	if len(c.All()) != before+n {
		return fmt.Errorf("row count changed")
	}
	fmt.Printf("synthetic catalog added=%d bytes=%d\n", n, len(sealed))
	return nil
}
