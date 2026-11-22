package main

import (
	"bytes"
	"encoding/json"
	"image/png"
	"strings"
	"testing"
	"time"
)

// allTextOf collects every string in a message's components (text, IDs, URLs).
func allTextOf(comps any) string {
	raw, _ := json.Marshal(comps)
	var v any
	json.Unmarshal(raw, &v)
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			b.WriteString(x + "\n")
		}
	}
	walk(v)
	return b.String()
}

// hourOf makes samples: one every 5 minutes for an hour, on a map, with n players.
func hourOf(start int64, m string, n, reported, slots int) []Sample {
	var out []Sample
	for i := int64(0); i < 12; i++ {
		out = append(out, Sample{At: start + i*300, Players: n, Reported: reported, Max: slots, Map: m})
	}
	return out
}

func TestSummarize(t *testing.T) {
	base := int64(1_700_000_000) / 3600 * 3600 // on the hour
	var s []Sample
	s = append(s, hourOf(base, "ze_a", 10, 10, 64)...)
	s = append(s, Sample{At: base + 3600, Players: -1}) // offline: not counted, doesn't split nothing
	s = append(s, hourOf(base+3900, "ze_b", 60, 60, 64)...)
	s = append(s, hourOf(base+3900+3600, "ze_a", 30, 46, 64)...)
	sum := summarize(s, 300)

	if sum.Avg != (10*12+60*12+30*12)/36 {
		t.Errorf("average = %d", sum.Avg)
	}
	if sum.Full != time.Hour { // only ze_b's hour has 58+ of 64
		t.Errorf("full = %v", sum.Full)
	}
	if len(sum.Runs) != 3 || sum.Runs[0].Map != "ze_a" || sum.Runs[1].Map != "ze_b" || sum.Runs[2].Map != "ze_a" {
		t.Fatalf("runs = %+v", sum.Runs)
	}
	if r := sum.Runs[1]; r.Avg != 60 || r.Most != 60 || r.End-r.Start != 3600 {
		t.Errorf("ze_b run = %+v", r)
	}
	if sum.Max != 64 {
		t.Errorf("max = %d", sum.Max)
	}
	// Busiest hour is ze_b's: base+1h05 falls in hour base+1h.
	if got, want := (sum.BusyHour/3600)%24, ((base+3900)/3600)%24; got != want {
		t.Errorf("busiest hour %d, want %d", got, want)
	}
	if sum.BusyHour > s[len(s)-1].At {
		t.Error("busiest hour should be a time already passed")
	}
}

func TestDrawChart(t *testing.T) {
	now := int64(1_700_000_000)
	from := now - 24*3600
	var s []Sample
	for at := from; at < now; at += 300 {
		p := Sample{At: at, Players: int((at / 300) % 64), Reported: 64, Max: 64, Map: "ze_x"}
		if at > from+3600 && at < from+7200 {
			p.Players = -1
		}
		s = append(s, p)
	}
	run := summarize(s, 300).Runs[0]
	img, err := png.Decode(bytes.NewReader(drawChart(s, from, now, 300, 64, &run)))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != chartW || b.Dy() != chartH {
		t.Fatalf("size %v", b)
	}
	if drawChart(nil, from, now, 300, 0, nil) == nil {
		t.Fatal("an empty chart still draws")
	}
}

func TestBusyView(t *testing.T) {
	// Saturdays 20:00 UTC+7 full, everything else nearly empty.
	sat := time.Date(2026, 11, 21, 20, 0, 0, 0, time.FixedZone("", 7*3600)).Unix()
	var s []Sample
	s = append(s, hourOf(sat, "ze_a", 60, 60, 64)...)
	s = append(s, hourOf(sat-24*3600, "ze_a", 2, 2, 64)...)
	got := busyView("th", s)
	for _, want := range []string{"🟥", "ส. 20:00–22:00", "UTC+7"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if busyView("en-US", nil) != T("en-US", "stat.collecting") {
		t.Error("no data should say so")
	}
}

func TestRecordAndPruneSamples(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	oldDB := db
	db = s
	defer func() { db = oldDB; lastSampleSlot = 0 }()
	id, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: "1.1.1.1:27015"})
	byAddr := map[string][]Server{"1.1.1.1:27015": {{ID: id}}, "2.2.2.2:27015": nil}
	res := map[string]*Status{"1.1.1.1:27015": {Map: "ze_a", Players: 5, Max: 64, Reported: 5}}
	now := time.Unix(1_700_000_100, 0)
	lastSampleSlot = 0
	recordSamples(byAddr, res, now)
	recordSamples(byAddr, res, now.Add(time.Minute)) // same 5-minute slot: no new row
	got, _ := s.Samples("1.1.1.1:27015", 0)
	if len(got) != 1 || got[0].Players != 5 || got[0].At%300 != 0 {
		t.Fatalf("samples = %+v", got)
	}
	if other, _ := s.Samples("2.2.2.2:27015", 0); len(other) != 0 {
		t.Fatal("a missed reply isn't offline yet: no sample")
	}
	s.AddSample("9.9.9.9:27015", Sample{At: now.Unix()}) // not tracked anywhere
	s.AddSample("1.1.1.1:27015", Sample{At: now.Add(-40 * 24 * time.Hour).Unix()})
	if err := s.PruneSamples(now.Add(-statKeep).Unix()); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Samples("1.1.1.1:27015", 0); len(got) != 1 {
		t.Fatalf("old history should be pruned: %+v", got)
	}
	if got, _ := s.Samples("9.9.9.9:27015", 0); len(got) != 0 {
		t.Fatal("history of untracked servers should be pruned")
	}
}

func TestStatView(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	oldDB := db
	db = s
	defer func() { db = oldDB }()
	sv := Server{ID: 7, Address: "1.1.1.1:27015", Name: "Test"}
	now := time.Unix(1_700_000_000, 0)
	if comps, file := statView("en-US", sv, "24h", 0, now); file != nil || !strings.Contains(allTextOf(comps), "Not enough history") {
		t.Fatal("no history yet: a note, no chart")
	}
	for _, x := range hourOf(now.Unix()-3600, "ze_a", 20, 20, 64) {
		s.AddSample(sv.Address, x)
	}
	comps, file := statView("en-US", sv, "24h", now.Unix()-3600, now)
	text := allTextOf(comps)
	if file == nil {
		t.Fatalf("chart view has no picture: %s", text)
	}
	for _, want := range []string{"attachment://stat.png", "**20** average", "ze\\_a", "stat:busy:7", "stat:map:7:24h"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if _, file := statView("en-US", sv, "busy", 0, now); file != nil {
		t.Error("busy hours has no picture")
	}
}
