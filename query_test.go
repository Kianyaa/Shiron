package main

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
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

func TestCardTimes(t *testing.T) {
	if got := mapStarted("en-US", &Status{}); got != "" {
		t.Fatalf("unknown start: %q", got)
	}
	// A server faking 16 players while listing 1: the card shows 1, with no warning.
	st := &Status{Map: "ze_x", Players: 1, Max: 65, Claimed: 16, MapSince: 1700000000}
	card := fullCard("en-US", Server{Address: "1.2.3.4:27015"}, st, "")
	text := ""
	for _, c := range card.Components {
		if td, ok := c.(discordgo.TextDisplay); ok {
			text += td.Content + "\n"
		}
	}
	for _, want := range []string{"**1 / 65**", "Map started at <t:1700000000:t> (<t:1700000000:R>)", "updated at <t:"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "16") {
		t.Errorf("the fake count shouldn't show: %q", text)
	}
}
