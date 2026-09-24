package desk

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bprendie/weazlcloud/internal/takeout"
	"github.com/bprendie/weazlcloud/internal/vault"
)

type takeoutJob struct {
	Owner   string          `json:"-"`
	Name    string          `json:"name"`
	Status  string          `json:"status"`
	Summary takeout.Summary `json:"summary"`
	Error   string          `json:"error,omitempty"`
	Updated time.Time       `json:"updated_at"`
	cancel  context.CancelFunc
}

func (h *Handler) takeoutOwner(w http.ResponseWriter, r *http.Request) (string, bool) {
	u, err := h.users.Current(r)
	if err != nil {
		apiUsersError(w, err)
		return "", false
	}
	if h.importDir == "" || h.importOwner == "" || !strings.EqualFold(u.Username, h.importOwner) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Takeout staging is unavailable for this account"})
		return "", false
	}
	return u.ID, true
}

func (h *Handler) listTakeout(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.takeoutOwner(w, r)
	if !ok {
		return
	}
	entries, err := os.ReadDir(h.importDir)
	if err != nil {
		apiError(w, err)
		return
	}
	type source struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	archives := make([]source, 0)
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.EqualFold(strings.ToLower(entry.Name()[max(0, len(entry.Name())-4):]), ".zip") {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			archives = append(archives, source{entry.Name(), info.Size()})
		}
	}
	h.importMu.Lock()
	jobs := make([]takeoutJob, 0)
	for _, job := range h.importJobs {
		if job.Owner == owner {
			jobs = append(jobs, takeoutJob{Owner: job.Owner, Name: job.Name, Status: job.Status, Summary: job.Summary, Error: job.Error, Updated: job.Updated})
		}
	}
	h.importMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"archives": archives, "jobs": jobs})
}

func (h *Handler) startTakeout(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.takeoutOwner(w, r)
	if !ok {
		return
	}
	res, u, err := h.currentResource(r)
	if err != nil {
		apiUsersError(w, err)
		return
	}
	if !res.Vault.Unlocked() {
		apiError(w, vault.ErrLocked)
		return
	}
	var body struct {
		Name        string `json:"name"`
		SkipCorrupt bool   `json:"skip_corrupt"`
	}
	if !decodeBody(w, r, &body, 8192) {
		return
	}
	f, z, err := takeout.Open(h.importDir, body.Name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	summary, err := takeout.Scan(body.Name, z, "Google Takeout")
	if err != nil {
		f.Close()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if summary.Files == 0 {
		f.Close()
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ZIP contains no files"})
		return
	}
	h.importMu.Lock()
	for _, active := range h.importJobs {
		if active.Status == "running" {
			h.importMu.Unlock()
			f.Close()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another Takeout import is running"})
			return
		}
	}
	jobCtx, release, admitted := h.registry.Enter(context.Background(), owner)
	if !admitted {
		h.importMu.Unlock()
		f.Close()
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "account unavailable"})
		return
	}
	jobCtx, cancel := context.WithCancel(jobCtx)
	job := &takeoutJob{Owner: owner, Name: body.Name, Status: "running", Summary: summary, Updated: time.Now().UTC(), cancel: cancel}
	h.importJobs[body.Name] = job
	h.importMu.Unlock()
	go func() {
		defer release()
		defer cancel()
		defer f.Close()
		reserve := func(bytes int64) (func(), error) { return h.quota.Reserve(u.ID, h.users.Count(), 0, 0, bytes) }
		progress := func(s takeout.Summary) {
			h.importMu.Lock()
			job.Summary = s
			job.Updated = time.Now().UTC()
			h.importMu.Unlock()
		}
		result, err := takeout.Import(jobCtx, res.Lib, body.Name, z, "Google Takeout", reserve, progress, takeout.Options{SkipCorrupt: body.SkipCorrupt})
		h.importMu.Lock()
		job.Summary = result
		job.Updated = time.Now().UTC()
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
		} else {
			job.Status = "complete"
		}
		h.importMu.Unlock()
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"name": body.Name, "summary": summary, "status": "running"})
}

func (h *Handler) cancelTakeout(w http.ResponseWriter, r *http.Request) {
	owner, ok := h.takeoutOwner(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	h.importMu.Lock()
	job := h.importJobs[body.Name]
	if job == nil || job.Owner != owner || job.Status != "running" {
		h.importMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "running import not found"})
		return
	}
	job.cancel()
	h.importMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "cancelled"})
}

func (h *Handler) cancelOwnerTakeout(owner string) {
	h.importMu.Lock()
	defer h.importMu.Unlock()
	for _, job := range h.importJobs {
		if job.Owner == owner && job.Status == "running" {
			job.cancel()
		}
	}
}
