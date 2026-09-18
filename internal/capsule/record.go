package capsule

import (
	"errors"
	"time"
)

var (
	ErrGone     = errors.New("this grab is gone")
	ErrPhrase   = errors.New("incorrect passphrase")
	ErrNeedBase = errors.New("set an https grab base in Places before minting")
	ErrNeedPath = errors.New("pick a file or a folder first")
)

type Member struct {
	Title string `json:"title"`
	Size  int64  `json:"size"`
	Kind  string `json:"kind"`
}

type Record struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Gate    string    `json:"gate"`
	Expires time.Time `json:"expires"`
	Limit   int       `json:"limit"`
	Used    int       `json:"used"`
	Revoked bool      `json:"revoked"`
	Size    int64     `json:"size"`
	Files   []Member  `json:"files,omitempty"`
}

func (r Record) Live() bool {
	if r.Revoked || r.Used >= r.Limit {
		return false
	}
	return time.Now().Before(r.Expires)
}

func (r Record) View() map[string]any {
	status := "live"
	if !r.Live() {
		status = "burned"
	}
	left := r.Limit - r.Used
	if left < 0 {
		left = 0
	}
	return map[string]any{
		"id": r.ID, "label": r.Label, "name": r.Name, "kind": r.Kind,
		"gate": r.Gate, "expires": r.Expires, "left": left, "status": status,
	}
}
