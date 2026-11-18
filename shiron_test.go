package main

import (
	"strings"
	"testing"
)

func TestLocalesComplete(t *testing.T) {
	for key, en := range locales["en"] {
		for _, l := range []string{"th", "zh-CN"} {
			msg, ok := locales[l][key]
			if !ok {
				t.Errorf("%s missing key %q", l, key)
				continue
			}
			if strings.Count(msg, "%") != strings.Count(en, "%") {
				t.Errorf("%s %q: placeholder count differs from en", l, key)
			}
		}
	}
	for _, l := range []string{"th", "zh-CN"} {
		for key := range locales[l] {
			if _, ok := locales["en"][key]; !ok {
				t.Errorf("%s has extra key %q", l, key)
			}
		}
	}
}

func TestCommandDescriptionsFitDiscord(t *testing.T) {
	// Discord rejects descriptions over 100 characters, in any language.
	check := func(name, desc string, loc map[string]string) {
		if n := len([]rune(desc)); n == 0 || n > 100 {
			t.Errorf("%s: en description has %d chars", name, n)
		}
		for l, d := range loc {
			if n := len([]rune(d)); n == 0 || n > 100 {
				t.Errorf("%s: %s description has %d chars", name, l, n)
			}
		}
	}
	for _, c := range commandDefs() {
		loc := map[string]string{}
		for k, v := range *c.DescriptionLocalizations {
			loc[string(k)] = v
		}
		check(c.Name, c.Description, loc)
		opts := c.Options
		for _, o := range c.Options {
			opts = append(opts, o.Options...) // subcommand options
		}
		for _, o := range opts {
			loc := map[string]string{}
			for k, v := range o.DescriptionLocalizations {
				loc[string(k)] = v
			}
			check(c.Name+" "+o.Name, o.Description, loc)
		}
	}
}

func TestLang(t *testing.T) {
	for in, want := range map[string]string{"th": "th", "zh-CN": "zh-CN", "zh-TW": "en", "en-US": "en", "": "en", "ja": "en"} {
		if got := lang(in); got != want {
			t.Errorf("lang(%q) = %q, want %q", in, got, want)
		}
	}
	if T("th", "nope.missing") != "nope.missing" {
		t.Error("missing key should return the key")
	}
}

func TestChanged(t *testing.T) {
	cases := []struct {
		prev, cur string
		want      bool
	}{
		{"de_dust2", "de_mirage", true},
		{"de_dust2", "de_dust2", false},
		{"de_dust2", "DE_DUST2", false},
		{"", "de_dust2", false}, // first sighting
		{"de_dust2", "", false}, // bad reply
	}
	for _, c := range cases {
		if got := changed(c.prev, c.cur); got != c.want {
			t.Errorf("changed(%q,%q) = %v", c.prev, c.cur, got)
		}
	}
}

func TestMatchMap(t *testing.T) {
	cases := []struct {
		pattern, m string
		want       bool
	}{
		{"de_dust2", "de_dust2", true},
		{"de_dust2", "DE_Dust2", true},
		{"de_*", "de_inferno", true},
		{"de_*", "cs_office", false},
		{"*", "anything", true},
		{"de_dust2", "de_dust2_night", false},
	}
	for _, c := range cases {
		if got := matchMap(c.pattern, c.m); got != c.want {
			t.Errorf("matchMap(%q,%q) = %v", c.pattern, c.m, got)
		}
	}
}

func TestResolveAddr(t *testing.T) {
	ok := map[string]string{
		"1.1.1.1":                 "1.1.1.1:27015",
		" 1.1.1.1:27016 ":         "1.1.1.1:27016",
		"[2606:4700::1111]:27015": "[2606:4700::1111]:27015",
	}
	for in, want := range ok {
		if got, err := resolveAddr(in); err != nil || got != want {
			t.Errorf("resolveAddr(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"127.0.0.1", "10.0.0.5:27015", "192.168.1.10", "172.16.0.1", "100.64.1.1",
		"169.254.169.254", "0.0.0.0", "::1", "[::1]:27015", "1.1.1.1:0", "1.1.1.1:99999", "1.1.1.1:abc", ""} {
		if got, err := resolveAddr(in); err == nil {
			t.Errorf("resolveAddr(%q) = %q, want error", in, got)
		}
	}
}

func TestStore(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	id, err := s.AddServer(Server{GuildID: "g1", ChannelID: "c1", Address: "1.1.1.1:27015", LastMap: "de_dust2", Name: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddServer(Server{GuildID: "g1", ChannelID: "c2", Address: "1.1.1.1:27015"}); !isUnique(err) {
		t.Fatalf("duplicate add: want UNIQUE error, got %v", err)
	}
	if _, err := s.AddServer(Server{GuildID: "g2", ChannelID: "c9", Address: "1.1.1.1:27015"}); err != nil {
		t.Fatalf("same address in another guild should work: %v", err)
	}
	if all, _ := s.Servers(""); len(all) != 2 {
		t.Fatalf("Servers(\"\") = %d, want 2", len(all))
	}

	// Another guild can't touch g1's server.
	if ok, _ := s.SetMinPlayers("g2", id, 5); ok {
		t.Fatal("cross-guild update should not apply")
	}
	if ok, _ := s.SetMinPlayers("g1", id, 5); !ok {
		t.Fatal("SetMinPlayers failed")
	}
	if sv, _ := s.Server("g1", id); sv.MinPlayers != 5 {
		t.Fatalf("MinPlayers = %d", sv.MinPlayers)
	}
	if err := s.UpdateState(id, "de_mirage", "Renamed"); err != nil {
		t.Fatal(err)
	}
	if sv, _ := s.Server("g1", id); sv.LastMap != "de_mirage" || sv.Label() != "Renamed (1.1.1.1:27015)" {
		t.Fatalf("UpdateState not saved: %+v", sv)
	}

	if err := s.AddNotify("u1", id, "de_*"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNotify("u1", id, "de_*"); !isUnique(err) {
		t.Fatalf("duplicate notify: want UNIQUE error, got %v", err)
	}
	if n, _ := s.Notifies("u1", "g1"); len(n) != 1 || n[0].Server.Address != "1.1.1.1:27015" {
		t.Fatalf("Notifies = %+v", n)
	}
	if n, _ := s.NotifiesFor(id); len(n) != 1 {
		t.Fatalf("NotifiesFor = %d", len(n))
	}

	if d := s.Delivery("u1"); d != "dm" {
		t.Fatalf("default delivery = %q", d)
	}
	s.SetDelivery("u1", "channel")
	s.SetDelivery("u1", "channel") // upsert twice
	if d := s.Delivery("u1"); d != "channel" {
		t.Fatalf("delivery = %q", d)
	}

	// Removing a server removes its notifications (ON DELETE CASCADE).
	if ok, _ := s.RemoveServer("g1", id); !ok {
		t.Fatal("RemoveServer failed")
	}
	if n, _ := s.NotifiesFor(id); len(n) != 0 {
		t.Fatal("notifications not cascaded")
	}
	if err := s.DeleteGuild("g2"); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Servers(""); len(all) != 0 {
		t.Fatalf("DeleteGuild left %d servers", len(all))
	}
}
