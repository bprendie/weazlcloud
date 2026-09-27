package takeout

import (
	"context"
	"testing"
)

func TestRootLayoutAndWhitespace(t *testing.T) {
	for raw, want := range map[string]string{
		"Takeout/Drive/":                     "",
		"Takeout/Drive/ Trip /a.jpg":         "Trip/a.jpg",
		"Takeout/Google Photos/ Trip /a.jpg": "Photos/Trip/a.jpg",
		"Takeout/Calendar/a.ics":             "Other/Calendar/a.ics",
	} {
		got, err := Destination("", raw)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", raw, got, err)
		}
	}
	for _, raw := range []string{"Takeout/Drive/ ../bad", "Takeout/Drive/ /bad", "Takeout/Drive/ ./bad", "Takeout/Drive"} {
		if _, err := Destination("", raw); err == nil {
			t.Fatalf("accepted unsafe/root file %q", raw)
		}
	}
	lib := parallelLibrary(t)
	for n, entries := range [][]zipEntry{
		{{"", ""}, {"Trip /one.txt", "first"}},
		{{"Trip /two.txt", "second"}},
	} {
		z := orderedZIP(t, entries...)
		s, err := Import(context.Background(), lib, "part.zip", z, "", nil, nil)
		if err != nil || s.Imported != 1 {
			t.Fatalf("part %d %+v %v", n, s, err)
		}
	}
	for _, name := range []string{"Trip/one.txt", "Trip/two.txt"} {
		if _, err := lib.Get(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Import(context.Background(), lib, "part.zip", orderedZIP(t, zipEntry{"Trip /two.txt", "second"}), "", nil, nil)
	if err != nil || s.Skipped != 1 {
		t.Fatalf("resume %+v %v", s, err)
	}
}
