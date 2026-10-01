package desk

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func smokePhotoSelectionArchive(t *testing.T, client *http.Client, base, id string, original []byte) {
	t.Helper()
	post := func(route string, body any, expected int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r, _ := http.NewRequest("POST", base+route, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Weazl-Desk", "1")
		res, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		reply, _ := io.ReadAll(res.Body)
		if res.StatusCode != expected {
			t.Fatalf("%s=%d %s", route, res.StatusCode, reply)
		}
		return reply
	}
	var selection struct {
		ID    string `json:"id"`
		Count int    `json:"count"`
	}
	json.Unmarshal(post("/api/v1/photos/selections", map[string]any{"ids": []string{id}}, 201), &selection)
	if selection.ID == "" || selection.Count != 1 {
		t.Fatal("server selection did not resolve")
	}
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(post("/api/v1/photos/selection-actions", map[string]any{"selection_id": selection.ID, "action": "archive"}, 202), &job)
	if job.ID == "" {
		t.Fatal("selection ZIP did not create a job")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res, err := client.Get(base + "/api/v1/photos/archives?id=" + job.ID)
		if err != nil {
			t.Fatal(err)
		}
		json.NewDecoder(res.Body).Decode(&job)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("ZIP status=%d", res.StatusCode)
		}
		if job.Status == "ready" {
			break
		}
		if job.Status == "failed" {
			t.Fatal("ZIP preparation failed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status != "ready" {
		t.Fatal("ZIP preparation timed out")
	}
	res, err := client.Get(base + "/api/v1/photos/archives?id=" + job.ID + "&download=1")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || res.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("ZIP download=%d %v", res.StatusCode, err)
	}
	z, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range z.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil || !bytes.Equal(raw, original) {
			t.Fatal("ZIP changed original")
		}
		found = true
	}
	if !found {
		t.Fatal("empty ZIP")
	}
	r, _ := http.NewRequest("GET", base+"/api/v1/photos/archives?id="+job.ID+"&download=1", nil)
	r.Header.Set("Range", "bytes=4-19")
	res, err = client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	part, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 206 || !bytes.Equal(part, body[4:20]) {
		t.Fatal("encrypted ZIP range read differs")
	}
}
