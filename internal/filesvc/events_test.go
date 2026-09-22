package filesvc

import (
	"testing"

	"github.com/bprendie/weazlcloud/internal/library"
)

func TestHubResyncsOnSubscribeAndPublishesChanges(t *testing.T) {
	hub := NewHub()
	stream, cancel := hub.Subscribe()
	defer cancel()
	initial := <-stream
	if initial.Version != 0 || initial.Change.Kind != "resync" {
		t.Fatalf("initial event = %+v", initial)
	}
	hub.Publish(library.Change{Kind: "put", Paths: []string{"photos/a.png"}})
	event := <-stream
	if event.Version != 1 || event.Change.Kind != "put" || len(event.Change.Paths) != 1 {
		t.Fatalf("published event = %+v", event)
	}
	if hub.CurrentVersion() != 1 {
		t.Fatalf("hub version = %d", hub.CurrentVersion())
	}
}
