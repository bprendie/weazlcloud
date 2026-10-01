package desk

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func smokeTakeoutPhotos(t *testing.T, c *http.Client, base string, post func(string, any) *http.Response) string {
	t.Helper()
	res, err := c.Get(base + "/api/photos?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	var photoPage struct {
		Items      []map[string]any `json:"items"`
		NextCursor string           `json:"next_cursor"`
	}
	if err = json.NewDecoder(res.Body).Decode(&photoPage); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(photoPage.Items) != 1 || photoPage.NextCursor == "" {
		t.Fatalf("photo page %+v status %d", photoPage, res.StatusCode)
	}
	photoID, _ := photoPage.Items[0]["id"].(string)
	if photoID == "" {
		t.Fatal("photo page omitted entry ID")
	}
	res, err = c.Get(base + "/api/library/thumbnail?id=" + url.QueryEscape(photoID) + "&size=320")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("photo ID thumbnail status %d", res.StatusCode)
	}
	res, err = c.Get(base + "/api/v1/photos/assets/" + url.PathEscape(photoID))
	if err != nil {
		t.Fatal(err)
	}
	var detail map[string]any
	if err = json.NewDecoder(res.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || detail["id"] != photoID {
		t.Fatalf("versioned photo detail=%v status=%d", detail, res.StatusCode)
	}
	res = post("/api/v1/photos/assets/"+url.PathEscape(photoID), map[string]bool{"favorite": true})
	var favorite map[string]any
	if err = json.NewDecoder(res.Body).Decode(&favorite); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || favorite["favorite"] != true {
		t.Fatalf("favorite update=%v status=%d", favorite, res.StatusCode)
	}
	res, err = c.Get(base + "/api/v1/photos?mode=favorites")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(res.Body).Decode(&photoPage); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(photoPage.Items) != 1 {
		t.Fatalf("favorites endpoint=%+v status=%d", photoPage, res.StatusCode)
	}
	res = post("/api/photos/preparation", map[string]string{"action": "start"})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("start photo preparation %d", res.StatusCode)
	}
	res = post("/api/photos/preparation", map[string]string{"action": "pause"})
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pause photo preparation %d", res.StatusCode)
	}
	res, err = c.Get(base + "/api/v1/photos?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(res.Body).Decode(&photoPage); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	aliceCursor := photoPage.NextCursor
	res, err = c.Get(base + "/api/photos?limit=1&cursor=" + url.QueryEscape(aliceCursor))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(res.Body).Decode(&photoPage); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || len(photoPage.Items) != 1 {
		t.Fatalf("photo page continuation %+v status %d", photoPage, res.StatusCode)
	}
	res = post("/api/v1/photos/folders", map[string]any{"path": "Photos/Trip", "hidden": true})
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("hide photo folder status=%d", res.StatusCode)
	}
	res.Body.Close()
	res, err = c.Get(base + "/api/v1/photos?mode=hidden")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.NewDecoder(res.Body).Decode(&photoPage); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	foundHidden := false
	for _, item := range photoPage.Items {
		if item["id"] == photoID {
			foundHidden = true
		}
	}
	if res.StatusCode != http.StatusOK || !foundHidden {
		t.Fatalf("hidden Photos query did not return hidden entry: %+v status=%d", photoPage, res.StatusCode)
	}
	res, err = c.Get(base + "/api/v1/photos/assets/" + url.PathEscape(photoID))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("normal Photos detail exposed hidden ID: %d", res.StatusCode)
	}
	res, err = c.Get(base + "/api/v1/photos/assets/" + url.PathEscape(photoID) + "?hidden=1")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Hidden Photos detail status=%d", res.StatusCode)
	}
	return aliceCursor
}
