package photos

import (
	"errors"
	"testing"
)

func TestSidecarTitlesAndAmbiguousCollisions(t *testing.T) {
	index := NewSidecarIndex()
	if err := index.Add("Photos/truncated.supplemental-metadata.json", []byte(`{"title":"very-long-original.jpg"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := index.Match("very-long-original.jpg")
	if err != nil || got != "Photos/truncated.supplemental-metadata.json" {
		t.Fatal(got, err)
	}
	_ = index.Add("Photos/collision(1).json", []byte(`{"title":"very-long-original.jpg"}`))
	if _, err := index.Match("very-long-original.jpg"); !errors.Is(err, ErrAmbiguousSidecar) {
		t.Fatal("ambiguous title guessed", err)
	}
	_ = index.Add("Photos/very-long-original.jpg.json", []byte(`{"title":"very-long-original.jpg"}`))
	got, err = index.Match("very-long-original.jpg")
	if err != nil || got != "Photos/very-long-original.jpg.json" {
		t.Fatal("exact did not win", got, err)
	}
	_ = index.Add("Photos/path.json", []byte(`{"title":"../another.jpg"}`))
	if _, err = index.Match("../another.jpg"); !errors.Is(err, ErrNoCaptureMetadata) {
		t.Fatal("traversal title accepted", err)
	}
}
