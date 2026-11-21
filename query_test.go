package main

import (
	"strings"
	"testing"
)

func TestNoteMap(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	oldDB := db
	db = s
	defer func() { db = oldDB }()
	const addr = "9.9.9.9:27015"
	mapStarts.Delete(addr)

	if got := noteMap(addr, "ze_a"); got != 0 {
		t.Fatalf("first sighting: the start time isn't known, got %d", got)
	}
	since := noteMap(addr, "ze_b")
	if since == 0 {
		t.Fatal("a change Shiron saw should have a start time")
	}
	if got := noteMap(addr, "ze_b"); got != since {
		t.Fatalf("same map keeps its start time: %d != %d", got, since)
	}
	// After a restart the time comes back from the database.
	mapStarts.Delete(addr)
	if got := noteMap(addr, "ze_b"); got != since {
		t.Fatalf("after restart: %d != %d", got, since)
	}
}

func TestCardNotes(t *testing.T) {
	if got := cardNotes("en-US", &Status{}); got != "" {
		t.Fatalf("nothing to note: %q", got)
	}
	got := cardNotes("en-US", &Status{Claimed: 16, MapSince: 1700000000})
	for _, want := range []string{"claims 16 players", "Map started at <t:1700000000:t> · <t:1700000000:R>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
