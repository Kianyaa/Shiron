package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/rumblefrog/go-a2s"
)

func TestCode(t *testing.T) {
	cases := map[string]string{
		"de_dust2":                          "`de_dust2`",
		"[Free skins](http://scam.example)": "`[Free skins](http://scam.example)`",
		"evil` @everyone **x**":             "`evil' @everyone **x**`",
		"two\nlines":                        "`two lines`",
		"  ":                                "`-`",
	}
	for in, want := range cases {
		if got := code(in); got != want {
			t.Errorf("code(%q) = %q, want %q", in, got, want)
		}
	}
	if n := len([]rune(code(strings.Repeat("a", 500)))); n != 102 {
		t.Errorf("code should cap at 100 chars + backticks, got %d", n)
	}
}

func TestMapImage(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg.MapImages = ""
	if mapImage("de_dust2") != "" {
		t.Error("pictures off should give no URL")
	}
	cfg.MapImages = "https://img.example/%s.png"
	imageCache.Store("de_dust2", imageEntry{"https://img.example/de_dust2.png", time.Now()}) // no network in tests
	if got := mapImage("DE_Dust2"); got != "https://img.example/de_dust2.png" {
		t.Errorf("mapImage = %q", got)
	}
	if got := mapImage("workshop/123/de_dust2"); got != "https://img.example/de_dust2.png" {
		t.Errorf("workshop map = %q", got)
	}
	for _, bad := range []string{"de_dust2?x=1", "a b", "map%2f", "", "de.dust"} {
		if got := mapImage(bad); got != "" {
			t.Errorf("mapImage(%q) = %q, want none", bad, got)
		}
	}
}

func TestWorkshopParsing(t *testing.T) {
	html := `<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=111&searchtext=x">
		<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=222"><a href="...filedetails/?id=111">`
	if ids := workshopIDs(html); len(ids) != 2 || ids[0] != "111" || ids[1] != "222" {
		t.Fatalf("workshopIDs = %v", ids)
	}
	items := []workshopItem{
		{Title: "ze_ancient_wrath_p (old)", AppID: 730, PreviewURL: "https://img/a"},
		{Title: "ze_ancient_wrath_p", AppID: 4000, PreviewURL: "https://img/gmod"},
		{Title: "ze_ancient_wrath_p", AppID: 730, PreviewURL: "http://img/insecure"},
		{Title: "ze_ancient_wrath_p", AppID: 730, PreviewURL: "https://img/reupload", Views: 76},
		{Title: " ZE_Ancient_Wrath_P ", AppID: 730, PreviewURL: "https://img/original", Views: 876},
	}
	got := pickPreviews(items, "ze_ancient_wrath_p")
	// Exact titles by views, then the tagged "(old)" title; other games and http never.
	if len(got) != 3 || got[0] != "https://img/original" || got[1] != "https://img/reupload" || got[2] != "https://img/a" {
		t.Fatalf("pickPreviews = %q, want original (most viewed) first", got)
	}
	if got := pickPreviews(items, "de_dust2"); len(got) != 0 {
		t.Fatalf("no match should give none, got %q", got)
	}

	// Tagged titles count, but an exact title still wins even with fewer views.
	tagged := []workshopItem{
		{Title: "[BangDream][MyGO]ze_bang_dream_mygo", AppID: 730, PreviewURL: "https://img/tagged", Views: 2676},
		{Title: "ze_bang_dream_mygo", AppID: 730, PreviewURL: "https://img/exact", Views: 10},
		{Title: "ze_bang_dream_mygo_v2", AppID: 730, PreviewURL: "https://img/v2", Views: 9999},
	}
	got = pickPreviews(tagged, "ze_bang_dream_mygo")
	if len(got) != 3 || got[0] != "https://img/exact" || got[1] != "https://img/v2" || got[2] != "https://img/tagged" {
		t.Fatalf("tagged pickPreviews = %q", got)
	}
	for title, want := range map[string]int{
		"ze_x": 2, "ZE_X": 2, "[Tag] ze_x": 1, "ze_x (fixed)": 1, "[A]ze_x": 1,
		"ze_x_v2": 1, "aze_x": 0, "ze_xyz": 0, "nothing": 0, "ze_x2 and ze_x": 1, "ZE X": 1,
		"x_ze": 0, "ze": 0,
	} {
		if s := titleMatch(title, "ze_x"); s != want {
			t.Errorf("titleMatch(%q) = %d, want %d", title, s, want)
		}
	}
}

func TestPlayerBarAndAccent(t *testing.T) {
	for _, c := range []struct {
		humans, max int
		bar         string
	}{
		{0, 64, "⬛⬛⬛⬛⬛⬛⬛⬛⬛⬛"},
		{1, 64, "🟨⬛⬛⬛⬛⬛⬛⬛⬛⬛"}, // anyone playing shows
		{19, 64, "🟨🟨🟨⬛⬛⬛⬛⬛⬛⬛"},
		{40, 64, "🟩🟩🟩🟩🟩🟩🟩⬛⬛⬛"}, // half full or more turns green
		{70, 64, "🟩🟩🟩🟩🟩🟩🟩🟩🟩🟩"}, // over max (reserved slots)
		{0, 0, "⬛⬛⬛⬛⬛⬛⬛⬛⬛⬛"},
	} {
		if got := playerBar(&Status{Players: c.humans, Max: c.max}, 10); got != c.bar {
			t.Errorf("playerBar(%d,%d) = %s, want %s", c.humans, c.max, got, c.bar)
		}
	}
	if accent(&Status{Players: 40, Max: 64}) != colorBusy || accent(&Status{Players: 3, Max: 64}) != colorQuiet ||
		accent(&Status{Players: 5, Bots: 5, Max: 64}) != colorEmpty {
		t.Error("accent colors wrong (bots must not count)")
	}
}

func TestPlain(t *testing.T) {
	got := plain("[Free](https://scam.example) @everyone **x** <#1>\nline")
	for _, bad := range []string{"](", "https://", "@everyone", "**", "<#", "\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("plain left %q in %q", bad, got)
		}
	}
	if plain("[JP] Zombie Escape :: POSSESSION") != `\[JP\] Zombie Escape :: POSSESSION` {
		t.Errorf("plain changed normal text: %q", plain("[JP] Zombie Escape :: POSSESSION"))
	}
}

func TestServerCard(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg = Config{} // no pictures, no Join button: no network
	sv := Server{ID: 7, Address: "1.2.3.4:27015"}
	st := &Status{Name: "Test", Map: "ze_x", Players: 19, Max: 64}

	card := serverCard("en-US", sv, st, "ze_old")
	if *card.AccentColor != colorQuiet || len(card.Components) != 3 {
		t.Fatalf("card = %+v", card)
	}
	head := card.Components[0].(discordgo.TextDisplay).Content
	if !strings.Contains(head, "`ze_old`") || !strings.Contains(head, `## ze\_x`) {
		t.Errorf("head = %q", head)
	}
	row := card.Components[2].(discordgo.ActionsRow)
	// Every card, map-change posts included: Copy IP, Players, Refresh, Notify.
	if len(row.Components) != 4 || row.Components[2].(discordgo.Button).CustomID != "srv:refresh:7" ||
		row.Components[3].(discordgo.Button).CustomID != "srv:notify:7" {
		t.Errorf("map-change post buttons = %+v", row.Components)
	}

	cfg.ConnectURL = "https://example.github.io/shiron/connect.html"
	row = serverCard("en-US", sv, st, "").Components[2].(discordgo.ActionsRow)
	if b := row.Components[0].(discordgo.Button); b.Style != discordgo.LinkButton || !strings.HasSuffix(b.URL, "#1.2.3.4:27015") {
		t.Errorf("join button = %+v", b)
	}

	withList := withText(card, "list")
	if len(withList.Components) != 4 || withList.Components[2].(discordgo.TextDisplay).Content != "list" ||
		len(card.Components) != 3 {
		t.Errorf("withText should insert above buttons without touching the original: %+v", withList.Components)
	}

	// Compact: one text block and two buttons (Join, More).
	sv.Style = "compact"
	compact := serverCard("en-US", sv, st, "ze_old")
	if len(compact.Components) != 2 {
		t.Fatalf("compact = %+v", compact.Components)
	}
	text := compact.Components[0].(discordgo.TextDisplay).Content
	if !strings.Contains(text, `### ze\_x`) || !strings.Contains(text, "🟨🟨⬛⬛⬛  **19 / 64**") || !strings.Contains(text, "`ze_old`") {
		t.Errorf("compact text = %q", text)
	}
	row = compact.Components[1].(discordgo.ActionsRow)
	if len(row.Components) != 3 || row.Components[1].(discordgo.Button).CustomID != "srv:more:7" ||
		row.Components[2].(discordgo.Button).CustomID != "srv:notify:7" {
		t.Errorf("compact buttons = %+v", row.Components)
	}
	cfg.ConnectURL = "" // without a join page, Copy IP takes Join's place
	row = serverCard("en-US", sv, st, "").Components[1].(discordgo.ActionsRow)
	if row.Components[0].(discordgo.Button).CustomID != "srv:ip:7" {
		t.Errorf("compact without join = %+v", row.Components)
	}
}

func TestSkeletonCard(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg = Config{LoadingEmoji: "⏳"}
	sv := Server{ID: 3, Address: "9.9.9.9:27015", Name: "Test", LastMap: "ze_skel"}
	imageCache.Store("ze_skel", imageEntry{"https://img.example/ze_skel.png", time.Now()})
	lastSeen.Store(sv.Address, time.Unix(1700000000, 0))

	card := skeletonCard("en-US", sv)
	if *card.AccentColor != colorEmpty || len(card.Components) != 4 {
		t.Fatalf("skeleton = %+v", card.Components)
	}
	if _, ok := card.Components[0].(discordgo.MediaGallery); !ok {
		t.Error("cached picture should be shown")
	}
	status := card.Components[2].(discordgo.TextDisplay).Content
	if status != "⏳ Checking live status… · last seen <t:1700000000:R>" {
		t.Errorf("status = %q", status)
	}
	for _, b := range card.Components[3].(discordgo.ActionsRow).Components {
		if !b.(discordgo.Button).Disabled {
			t.Error("skeleton buttons must be disabled")
		}
	}
	// Unknown server: no picture, no last seen, no network.
	card = skeletonCard("en-US", Server{ID: 4, Address: "8.8.8.8:27015"})
	if len(card.Components) != 3 || card.Components[1].(discordgo.TextDisplay).Content != "⏳ Checking live status…" {
		t.Errorf("unknown skeleton = %+v", card.Components)
	}
}

func TestPresenceAndBio(t *testing.T) {
	for _, c := range []struct {
		tracked, guilds int
		want            string
	}{
		{2, 2, "Tracking 2 servers on 2 Discord servers"},
		{1, 1, "Tracking 1 server on 1 Discord server"},
		{0, 3, "Tracking 0 servers on 3 Discord servers"},
	} {
		if got := presenceText(c.tracked, c.guilds); got != c.want {
			t.Errorf("presenceText = %q, want %q", got, c.want)
		}
	}
	sub := discordgo.ApplicationCommandOptionSubCommand
	cmds := []*discordgo.ApplicationCommand{
		{ID: "1", Name: "map"},
		{ID: "2", Name: "servers", Options: []*discordgo.ApplicationCommandOption{{Type: sub, Name: "add"}, {Type: sub, Name: "list"}}},
		{ID: "3", Name: "help"},
	}
	got := bioText("Use /map, /servers add and /help. Not /servers alone or /nope.", cmds)
	want := "Use </map:1>, </servers add:2> and </help:3>. Not /servers alone or /nope."
	if got != want {
		t.Errorf("bioText = %q", got)
	}
	if (&Status{AppID: 730}).IsCS2() != true || (&Status{AppID: 80}).IsCS2() {
		t.Error("IsCS2 wrong")
	}
}

func TestDefaultServer(t *testing.T) {
	list := []Server{{ID: 1, ChannelID: "a"}, {ID: 2, ChannelID: "b"}, {ID: 3, ChannelID: "b"}}
	if sv := defaultServer(list, "a"); sv == nil || sv.ID != 1 {
		t.Errorf("channel with one server should pick it, got %+v", sv)
	}
	if sv := defaultServer(list, "b"); sv != nil {
		t.Errorf("channel with two servers must ask, got %+v", sv)
	}
	if sv := defaultServer(list, "other"); sv != nil {
		t.Errorf("untracked channel with several servers must ask, got %+v", sv)
	}
	if sv := defaultServer(list[:1], "other"); sv == nil || sv.ID != 1 {
		t.Errorf("only server in the Discord server should be picked anywhere, got %+v", sv)
	}
}

func TestNotificationsMigration(t *testing.T) {
	// A database from before all-server alerts: alerts must survive the upgrade.
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE servers (id INTEGER PRIMARY KEY, guild_id TEXT NOT NULL, channel_id TEXT NOT NULL,
		address TEXT NOT NULL, min_players INTEGER NOT NULL DEFAULT 0, last_map TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '', UNIQUE(guild_id, address));
		CREATE TABLE notifications (id INTEGER PRIMARY KEY, user_id TEXT NOT NULL,
		server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE, pattern TEXT NOT NULL, UNIQUE(user_id, server_id, pattern));
		INSERT INTO servers (id, guild_id, channel_id, address) VALUES (5, 'g', 'c', '1.1.1.1:27015');
		INSERT INTO notifications (id, user_id, server_id, pattern) VALUES (42, 'u', 5, 'ze_*');`)
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // opening twice must be harmless
		s, err := openDB(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		n, err := s.Notifies("u", "g")
		if err != nil || len(n) != 1+i || n[0].ID != 42 || n[0].Server.ID != 5 || n[0].Pattern != "ze_*" {
			s.Close()
			t.Fatalf("after upgrade (open %d): %+v, %v", i, n, err)
		}
		if err := s.AddNotify("u", "g", 0, "ze_new"); i == 0 && err != nil {
			t.Fatalf("all-servers alert after upgrade: %v", err)
		}
		s.Close()
	}
}

func TestStyleMigration(t *testing.T) {
	// A database from before card styles existed must gain the column on open.
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`CREATE TABLE servers (id INTEGER PRIMARY KEY, guild_id TEXT NOT NULL, channel_id TEXT NOT NULL,
		address TEXT NOT NULL, min_players INTEGER NOT NULL DEFAULT 0, last_map TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL DEFAULT '', UNIQUE(guild_id, address));
		INSERT INTO servers (guild_id, channel_id, address) VALUES ('g', 'c', '1.1.1.1:27015');`)
	old.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // opening twice must not fail on the existing column
		s, err := openDB(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		list, _ := s.Servers("g")
		if len(list) != 1 || list[0].Style != "full" {
			t.Fatalf("old server style = %+v", list)
		}
		if i == 0 {
			if ok, _ := s.SetStyle("g", list[0].ID, "compact"); !ok {
				t.Fatal("SetStyle failed")
			}
			if sv, _ := s.ServerByID(list[0].ID); sv.Style != "compact" {
				t.Fatalf("style = %q", sv.Style)
			}
			s.SetStyle("g", list[0].ID, "full")
		}
		s.Close()
	}
}

func TestScoreboard(t *testing.T) {
	players := []*a2s.Player{
		{Name: "Kola", Score: 0, Duration: 13 * 60},
		{Name: "RiserSword", Score: 5, Duration: 27 * 60},
		{Name: "", Score: 99}, // still connecting: skipped
		{Name: "FX战士韭留美", Score: 0, Duration: 27 * 60},
		{Name: "evil`name", Score: 1, Duration: 4000},
		{Name: "みけしばみけしばみけしばみけしば", Score: 0, Duration: 20},
	}
	out := scoreboard("en-US", players)
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(out, "```\n"), "```"), "\n")
	want := []string{
		" #  SCORE    TIME  PLAYER",
		"",
		" 1      5     27m  RiserSword",
		" 2      1  1h 06m  evil'name",
		" 3      0     27m  FX战士韭留美",
		" 4      0     13m  Kola",
		" 5      0     <1m  みけしばみけしばみ…",
	}
	for i, w := range want {
		if w == "" {
			continue // divider
		}
		if lines[i] != w {
			t.Errorf("line %d = %q, want %q", i, lines[i], w)
		}
	}
	// Every column before the name lines up regardless of the name's script.
	for _, l := range lines[2 : len(lines)-1] {
		if dispWidth(l[:strings.Index(l, "m  ")+3]) != 19 {
			t.Errorf("columns misaligned: %q", l)
		}
	}
	if scoreboard("en-US", []*a2s.Player{{Name: ""}}) != "" {
		t.Error("no named players should give no table")
	}
	if dispWidth("ผู้เล่น") != 4 || dispWidth("分数") != 4 || dispWidth("abc") != 3 {
		t.Error("dispWidth wrong for Thai marks or CJK")
	}
}

func TestConfFile(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "shiron.conf")
	os.WriteFile(conf, []byte("# comment\n\nSHIRON_POLL = \"30s\"\r\nSHIRON_DB=from-file.db\n"), 0o600)
	t.Setenv("SHIRON_POLL", "")
	os.Unsetenv("SHIRON_POLL")
	t.Setenv("SHIRON_DB", "from-env.db") // real environment wins
	if err := loadConfFile(conf); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("SHIRON_POLL") != "30s" || os.Getenv("SHIRON_DB") != "from-env.db" {
		t.Fatalf("POLL=%q DB=%q", os.Getenv("SHIRON_POLL"), os.Getenv("SHIRON_DB"))
	}
	if err := loadConfFile(filepath.Join(dir, "missing.conf")); err != nil {
		t.Fatal("a missing file should be fine")
	}
	os.WriteFile(conf, []byte("PATH=C:\\evil\n"), 0o600)
	if err := loadConfFile(conf); err == nil {
		t.Fatal("non-SHIRON keys must be rejected")
	}
}

func TestHealth(t *testing.T) {
	h := newHealth()
	steps := []struct {
		answered bool
		want     string
	}{
		{true, ""},
		{false, ""}, {false, ""}, // a couple of lost replies: quiet
		{true, ""},                                   // recovered before the limit: still quiet
		{false, ""}, {false, ""}, {false, "offline"}, // third miss in a row
		{false, ""}, {false, ""}, // stays offline without repeating
		{true, "online"},
		{true, ""},
	}
	start := time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC)
	for i, s := range steps {
		now := start.Add(time.Duration(i) * time.Minute) // one check a minute
		got, since := h.update("a", s.answered, now)
		if got != s.want {
			t.Fatalf("step %d: got %q, want %q", i, got, s.want)
		}
		// The last answer before going down was step 3 (20:03), both when it goes offline and when it's back.
		if got != "" && !since.Equal(start.Add(3*time.Minute)) {
			t.Errorf("step %d: since = %v, want 20:03", i, since)
		}
	}
	if got := statusText("en-US", "online", "S", start.Add(3*time.Minute), start.Add(9*time.Minute)); got != "🟢 **S** is back online · was offline for 6m" {
		t.Errorf("online text = %q", got)
	}
	if got := statusText("en-US", "offline", "S", start, start); got != fmt.Sprintf("🔴 **S** has gone offline · last seen <t:%d:R>", start.Unix()) {
		t.Errorf("offline text = %q", got)
	}
	if got := statusText("en-US", "offline", "S", time.Time{}, start); got != "🔴 **S** has gone offline" {
		t.Errorf("unknown last-seen should be left out: %q", got)
	}
	day := 24 * time.Hour
	for d, want := range map[time.Duration]string{
		-5 * time.Minute:                 "<1m", // clock jumped backwards
		0:                                "<1m",
		59 * time.Second:                 "<1m",
		time.Minute:                      "1m",
		6 * time.Minute:                  "6m",
		59*time.Minute + 59*time.Second:  "59m",
		time.Hour:                        "1h 00m",
		66 * time.Minute:                 "1h 06m",
		23*time.Hour + 59*time.Minute:    "23h 59m",
		day:                              "1d 0h",
		51 * time.Hour:                   "2d 3h",
		364*day + 23*time.Hour:           "364d 23h",
		365 * day:                        "1y 0d",
		400 * day:                        "1y 35d",
		3*365*day + 10*day + 5*time.Hour: "3y 10d",
		time.Duration(1<<63 - 1):         "292y 171d", // the largest duration Go can hold
	} {
		if got := downtime(d); got != want {
			t.Errorf("downtime(%v) = %q, want %q", d, got, want)
		}
	}
	// Down since before Shiron started: nothing known, no made-up time.
	h2 := newHealth()
	var since time.Time
	for i := 0; i < offlineAfter; i++ {
		_, since = h2.update("b", false, start)
	}
	if !since.IsZero() {
		t.Errorf("never-seen server: since = %v, want zero", since)
	}
	// After a restart, the first answer is only a baseline (no "map changed" post).
	if !h.firstAnswer("a") || h.firstAnswer("a") {
		t.Error("firstAnswer should be true once, then false")
	}
	h.update("gone", false, start)
	h.firstAnswer("gone")
	h.forget(map[string][]Server{"a": nil})
	if _, ok := h.misses["gone"]; ok {
		t.Error("untracked addresses should be forgotten")
	}
	if h.answered["gone"] {
		t.Error("forget should clear answered too")
	}
}

func TestMapPictures(t *testing.T) {
	// The case that started this: Workshop title with a different separator.
	if titleMatch("ZE_AMDAPOR_KEEP", "ze_amdaporkeep") != 1 {
		t.Error("ZE_AMDAPOR_KEEP should match ze_amdaporkeep")
	}

	// "No picture" is retried after missRetry; found pictures are kept.
	old := cfg
	defer func() { cfg = old }()
	cfg.MapImages = "https://img.example/%s.png"
	imageCache.Store("ze_fresh_miss", imageEntry{"", time.Now()})
	if mapImage("ze_fresh_miss") != "" {
		t.Error("a fresh miss should be remembered without a lookup")
	}

	// Admin override wins over the automatic picture, per guild, and can be removed.
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldDB := db
	db = s
	defer func() { db = oldDB }()
	imageCache.Store("ze_over", imageEntry{"https://auto/ze_over.png", time.Now()})
	s.SetMapPicture("g1", "ze_over", "https://mine/ze_over.png")
	if got := mapPicture("g1", "ZE_Over"); got != "https://mine/ze_over.png" {
		t.Errorf("override = %q", got)
	}
	if got := cachedMapPicture("g2", "ze_over"); got != "https://auto/ze_over.png" {
		t.Errorf("other guild should see the automatic picture, got %q", got)
	}
	s.SetMapPicture("g1", "ze_over", "")
	if got := mapPicture("g1", "ze_over"); got != "https://auto/ze_over.png" {
		t.Errorf("after removing, got %q", got)
	}

	// A linked collection's picture is used before the automatic one, and only in that guild.
	s.AddCollection("g1", "777")
	s.AddCollection("g1", "777") // twice is fine
	if ids := s.Collections("g1"); len(ids) != 1 || ids[0] != "777" {
		t.Fatalf("Collections = %v", ids)
	}
	imageCache.Store("ze_col", imageEntry{"https://auto/ze_col.png", time.Now()})
	imageCache.Store("c:777:ze_col", imageEntry{"https://collection/ze_col.png", time.Now()})
	if got := mapPicture("g1", "ze_col"); got != "https://collection/ze_col.png" {
		t.Errorf("collection picture = %q", got)
	}
	if got := cachedMapPicture("g1", "ze_col"); got != "https://collection/ze_col.png" {
		t.Errorf("cached collection picture = %q", got)
	}
	if got := mapPicture("g2", "ze_col"); got != "https://auto/ze_col.png" {
		t.Errorf("other guild = %q", got)
	}
	s.ClearCollections("g1")
	if len(s.Collections("g1")) != 0 {
		t.Error("ClearCollections left some")
	}

	// Links admins may paste (no network needed for these).
	for link, ok := range map[string]bool{
		"https://cdn.example/pic.png": true,
		"http://cdn.example/pic.png":  false,
		"javascript:alert(1)":         false,
		"not a link":                  false,
	} {
		if _, got, _ := pictureFromLink(link); got != ok {
			t.Errorf("pictureFromLink(%q) ok = %v, want %v", link, got, ok)
		}
	}
	if m := workshopLinkRe.FindStringSubmatch("https://steamcommunity.com/sharedfiles/filedetails/?l=latam&id=3663857931"); m == nil || m[1] != "3663857931" {
		t.Errorf("workshop link with extra params not recognised: %v", m)
	}
}

func TestServersListAndPanel(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldDB, oldCfg := db, cfg
	db, cfg = s, Config{Poll: time.Minute}
	defer func() { db, cfg = oldDB, oldCfg }()

	var ids []int64
	for i := 1; i <= 7; i++ { // 7 servers: two pages of 5
		id, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: fmt.Sprintf("1.1.1.%d:27015", i), Name: fmt.Sprintf("S%d", i), LastMap: "ze_last"})
		ids = append(ids, id)
	}
	lastSeen.Store("1.1.1.1:27015", time.Now())
	lastStatus.Store("1.1.1.1:27015", &Status{Map: "ze_live", Players: 40, Max: 64})
	lastSeen.Store("1.1.1.2:27015", time.Now().Add(-time.Hour)) // long silent: offline

	page0 := listView("en-US", "g", 0)[0].(discordgo.Container).Components
	if len(page0) != 1+5+1 { // header, 5 rows, pager
		t.Fatalf("page 0 has %d parts", len(page0))
	}
	row1 := page0[1].(discordgo.Section)
	line := row1.Components[0].(discordgo.TextDisplay).Content
	if !strings.HasPrefix(line, "`#1` 🟢 **ze\\_live** · 40/64") {
		t.Errorf("row 1 = %q", line)
	}
	if b := row1.Accessory.(discordgo.Button); b.CustomID != fmt.Sprintf("srvm:open:%d:0", ids[0]) {
		t.Errorf("manage button = %q", b.CustomID)
	}
	if l := page0[2].(discordgo.Section).Components[0].(discordgo.TextDisplay).Content; !strings.Contains(l, "🔴 **Offline**") {
		t.Errorf("row 2 should be offline: %q", l)
	}
	if l := page0[3].(discordgo.Section).Components[0].(discordgo.TextDisplay).Content; !strings.Contains(l, "⚪ **ze\\_last**") {
		t.Errorf("unchecked row should show last map: %q", l)
	}
	pager := page0[6].(discordgo.ActionsRow).Components
	if !pager[0].(discordgo.Button).Disabled || pager[2].(discordgo.Button).Disabled {
		t.Error("on page 1, back must be disabled and next enabled")
	}
	page1 := listView("en-US", "g", 1)[0].(discordgo.Container).Components
	if len(page1) != 1+2+1 || !strings.HasPrefix(page1[1].(discordgo.Section).Components[0].(discordgo.TextDisplay).Content, "`#6`") {
		t.Errorf("page 2 should hold #6 and #7")
	}
	if len(listView("en-US", "g", 99)[0].(discordgo.Container).Components) != 4 {
		t.Error("an out-of-range page should clamp to the last page")
	}

	sv, _ := s.Server("g", ids[2])
	panel := panelView("en-US", *sv, 0)[0].(discordgo.Container).Components
	if txt := panel[0].(discordgo.TextDisplay).Content; !strings.HasPrefix(txt, "### `#3` S3") || !strings.Contains(txt, "Full card · min players 0 · 0 alerts") {
		t.Errorf("panel text = %q", txt)
	}
	if b := panel[2].(discordgo.ActionsRow).Components[1].(discordgo.Button); b.Label != "Switch to Compact" {
		t.Errorf("style button = %q", b.Label)
	}
	empty := listView("en-US", "empty", 0)[0].(discordgo.Container).Components
	if !strings.Contains(empty[0].(discordgo.TextDisplay).Content, "No CS2 servers") {
		t.Error("an empty guild should get the no-servers message")
	}
	// Map pictures and Collections are there even before any server is added.
	tools := empty[1].(discordgo.ActionsRow).Components
	if len(tools) != 2 || tools[0].(discordgo.Button).CustomID != "srvm:pic:0:0" || tools[1].(discordgo.Button).CustomID != "srvm:col:0:0" {
		t.Errorf("empty list tools = %+v", tools)
	}
	// With pages, the bottom row is the pager plus the two tools: 5, Discord's limit.
	if row := page0[6].(discordgo.ActionsRow).Components; len(row) != 5 {
		t.Errorf("bottom row has %d buttons, want 5", len(row))
	}
}

func TestVersionSuffixes(t *testing.T) {
	for in, want := range map[string]string{
		"zm_420_office_p":            "zm_420_office",
		"ze_ffxii_westersand_v7_t":   "ze_ffxii_westersand",
		"ze_minecraft_superflat_v1":  "ze_minecraft_superflat",
		"ze_bioshock_v6_cs2":         "ze_bioshock",
		"ze_obj_rampage_v1_2":        "ze_obj_rampage_v1_2", // "2" isn't a suffix we strip
		"ze_p":                       "ze_p",                // never down to just the prefix
		"ze_x":                       "ze_x",
		"ze_FAPescape_Rote_P":        "ze_fapescape_rote",
		"ze_random_escape_fixed2_b1": "ze_random_escape",
	} {
		if got := baseMapName(in); got != want {
			t.Errorf("baseMapName(%q) = %q, want %q", in, got, want)
		}
	}
	items := []workshopItem{
		{Title: "zm_420_office", AppID: 730, PreviewURL: "https://img/office", Views: 403},
		{Title: "zm_420", AppID: 730, PreviewURL: "https://img/wrong", Views: 9999},
	}
	if got := pickPreviews(items, "zm_420_office_p"); len(got) != 1 || got[0] != "https://img/office" {
		t.Errorf("suffix fallback = %q", got)
	}
	// The full name wins when an upload has it.
	items = append(items, workshopItem{Title: "zm_420_office_p", AppID: 730, PreviewURL: "https://img/patched", Views: 1})
	if got := pickPreviews(items, "zm_420_office_p"); len(got) != 1 || got[0] != "https://img/patched" {
		t.Errorf("full name should win: %q", got)
	}
}

func TestRefreshKeepsHistory(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg = Config{}
	sv := Server{ID: 9, Address: "1.2.3.4:27015"}
	card := serverCard("en-US", sv, &Status{Name: "Test", Map: "ze_games_p", Players: 1, Max: 64}, "")

	// Round-trip through JSON, the way the card comes back from Discord on a click.
	raw, _ := json.Marshal(map[string]any{"id": "m", "components": []discordgo.MessageComponent{card}})
	var msg discordgo.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if got := cardMap(msg.Components); got != "ze_games_p" {
		t.Fatalf("cardMap = %q", got)
	}

	out, ok := markOutdated(msg.Components, "-# note")
	if !ok {
		t.Fatal("markOutdated refused a normal card")
	}
	box := out[0].(*discordgo.Container)
	n := len(box.Components)
	if td, ok := box.Components[n-2].(discordgo.TextDisplay); !ok || td.Content != "-# note" {
		t.Errorf("note not placed above the buttons: %#v", box.Components[n-2])
	}
	var left []string
	for _, b := range box.Components[n-1].(*discordgo.ActionsRow).Components {
		left = append(left, b.(*discordgo.Button).CustomID)
	}
	// Refresh and Players go; Copy IP and Notify stay on history cards.
	if strings.Join(left, ",") != "srv:ip:9,srv:notify:9" {
		t.Errorf("buttons left on an outdated card = %v", left)
	}
	if cardMap(out) != "ze_games_p" {
		t.Error("the card must still show its original map")
	}
	// It must still serialize for the edit request.
	if _, err := json.Marshal(out); err != nil {
		t.Fatalf("marshal outdated card: %v", err)
	}
	if _, ok := markOutdated(nil, "x"); ok {
		t.Error("no card should be refused")
	}

	// A map-change post keeps its "changed from" line when refreshed on the same map.
	post := serverCard("en-US", sv, &Status{Name: "Test", Map: "ze_lemonade", Players: 8, Max: 64}, "ze_saw_p")
	raw, _ = json.Marshal(map[string]any{"id": "m", "components": []discordgo.MessageComponent{post}})
	var postMsg discordgo.Message
	if err := json.Unmarshal(raw, &postMsg); err != nil {
		t.Fatal(err)
	}
	if got := cardPrevMap(postMsg.Components); got != "ze_saw_p" {
		t.Errorf("cardPrevMap = %q", got)
	}
	if got := cardMap(postMsg.Components); got != "ze_lemonade" {
		t.Errorf("cardMap on a map-change post = %q", got)
	}
	if got := cardPrevMap(msg.Components); got != "" {
		t.Errorf("a /map card has no previous map, got %q", got)
	}
}

func TestAlertListView(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldDB := db
	db = s
	defer func() { db = oldDB }()

	empty := alertListView("en-US", "u", "g", 0, "")[0].(discordgo.Container).Components
	if !strings.Contains(empty[0].(discordgo.TextDisplay).Content, "no map alerts") {
		t.Errorf("empty list = %+v", empty)
	}
	if b := empty[1].(discordgo.ActionsRow).Components[0].(discordgo.Button); b.CustomID != "ntf:mode:0:0" {
		t.Errorf("empty list should still offer the delivery switch: %+v", b)
	}

	sid, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: "1.1.1.1:27015", Name: "POSSESSION"})
	for i := 0; i < 6; i++ {
		server := sid
		if i == 0 {
			server = 0 // the first one covers all servers
		}
		s.AddNotify("u", "g", server, fmt.Sprintf("ze_map_%d", i))
	}
	page0 := alertListView("en-US", "u", "g", 0, "")[0].(discordgo.Container).Components
	if len(page0) != 1+5+1 {
		t.Fatalf("page 0 has %d parts", len(page0))
	}
	if h := page0[0].(discordgo.TextDisplay).Content; !strings.Contains(h, "Your map alerts · 6") || !strings.Contains(h, "Sent by DM") {
		t.Errorf("header = %q", h)
	}
	first := page0[1].(discordgo.Section)
	if txt := first.Components[0].(discordgo.TextDisplay).Content; txt != "`#1` `ze_map_0`\n-# all servers here" {
		t.Errorf("row 1 = %q", txt)
	}
	if b := first.Accessory.(discordgo.Button); b.Label != "Edit" || !strings.HasPrefix(b.CustomID, "ntf:open:") {
		t.Errorf("edit button = %+v", b)
	}
	if txt := page0[2].(discordgo.Section).Components[0].(discordgo.TextDisplay).Content; !strings.Contains(txt, "POSSESSION") {
		t.Errorf("row 2 should name its server: %q", txt)
	}
	row := page0[6].(discordgo.ActionsRow).Components
	if len(row) != 5 || row[3].(discordgo.Button).Label != "Send by mention in the server's channel instead" || row[4].(discordgo.Button).CustomID != "ntf:rmall:0:0" {
		t.Errorf("bottom row = %+v", row)
	}
	if other := alertListView("en-US", "someone-else", "g", 0, "")[0].(discordgo.Container).Components; len(other) != 2 {
		t.Error("another user must not see these alerts")
	}
}

func TestCommandSet(t *testing.T) {
	want := []string{"about", "help", "invite", "map", "notify add", "notify list", "ping", "players", "servers add", "servers list"}
	var got []string
	for _, c := range commandDefs() {
		if len(c.Options) > 0 && c.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
			for _, sub := range c.Options {
				got = append(got, c.Name+" "+sub.Name)
				if c.Name == "servers" && sub.Name == "add" && sub.Options[0].Name != "ip" {
					t.Errorf("/servers add first option = %q, want ip", sub.Options[0].Name)
				}
			}
			continue
		}
		got = append(got, c.Name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("commands = %v\nwant       %v", got, want)
	}
	// Every command has a handler and every handler has a command.
	for _, name := range got {
		if handlers[name] == nil {
			t.Errorf("no handler for /%s", name)
		}
	}
	if len(handlers) != len(got) {
		t.Errorf("%d handlers for %d commands", len(handlers), len(got))
	}
}

func TestChangelogAndAbout(t *testing.T) {
	// The real CHANGELOG.md must parse, newest first, newest is v1.0.2.
	v := version()
	if v.Version != "v1.0.2" || v.Date != "2026-11-21" || !strings.HasPrefix(v.Notes, "- ") {
		t.Fatalf("version() = %+v", v)
	}
	rs := releases("# Changelog\nintro\n\n## v1.1 · 2026-12-01\n- new thing\n\n## v1.0 · 2026-11-19\n- first\n")
	if len(rs) != 2 || rs[0].Version != "v1.1" || rs[0].Notes != "- new thing" || rs[1].Date != "2026-11-19" {
		t.Errorf("releases = %+v", rs)
	}

	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldDB := db
	db = s
	defer func() { db = oldDB }()
	s.AddServer(Server{GuildID: "g1", ChannelID: "c", Address: "1.1.1.1:27015"})
	s.AddServer(Server{GuildID: "g2", ChannelID: "c", Address: "1.1.1.1:27015"}) // same server, two guilds
	s.AddServer(Server{GuildID: "g2", ChannelID: "c", Address: "2.2.2.2:27015"})
	if n := trackedCount(); n != 2 {
		t.Errorf("trackedCount = %d, want 2 distinct servers", n)
	}

	sess, _ := discordgo.New("Bot test")
	sess.State.User = &discordgo.User{ID: "123", Username: "Shiron"}
	sess.State.Guilds = []*discordgo.Guild{{ID: "g1"}, {ID: "g2"}}
	box := aboutView("en-US", sess)[0].(discordgo.Container).Components
	var all strings.Builder
	for _, c := range box {
		switch v := c.(type) {
		case discordgo.TextDisplay:
			all.WriteString(v.Content + "\n")
		case discordgo.Section:
			all.WriteString(v.Components[0].(discordgo.TextDisplay).Content + "\n")
		}
	}
	for _, want := range []string{"## Shiron", "v1.0.2 · updated 2026-11-21", "What's new in v1.0.2", "**2** Discord servers", "**2** CS2 servers tracked", "online since <t:"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("about card missing %q:\n%s", want, all.String())
		}
	}
	row := box[len(box)-1].(discordgo.ActionsRow).Components
	if row[0].(discordgo.Button).Style != discordgo.LinkButton || row[1].(discordgo.Button).CustomID != "about:log" {
		t.Errorf("about buttons = %+v", row)
	}
	if txt := changelogView("en-US")[0].(discordgo.TextDisplay).Content; !strings.Contains(txt, "**v1.0.2** · 2026-11-21") {
		t.Errorf("changelog = %q", txt)
	}
}

func TestStopAlertsHelpers(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg = Config{}
	sv := Server{ID: 4, GuildID: "g", Address: "1.2.3.4:27015"}
	card := serverCard("en-US", sv, &Status{Name: "S", Map: "ze_x", Players: 1, Max: 64}, "")
	dm := withStopButton("en-US", card, sv, "ze_x")
	ids := func(c discordgo.Container) (out []string) {
		for _, b := range c.Components[len(c.Components)-1].(discordgo.ActionsRow).Components {
			out = append(out, b.(discordgo.Button).CustomID)
		}
		return
	}
	if got := strings.Join(ids(dm), ","); !strings.Contains(got, "srv:unnotify:4:ze_x") || strings.Contains(got, "srv:notify:") {
		t.Errorf("DM card buttons = %s", got)
	}
	// The channel post uses the same card value: it must keep Notify me.
	if got := strings.Join(ids(card), ","); !strings.Contains(got, "srv:notify:4") || strings.Contains(got, "unnotify") {
		t.Errorf("original card was changed: %s", got)
	}

	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oldDB := db
	db = s
	defer func() { db = oldDB }()
	sid, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: "1.2.3.4:27015"})
	other, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: "5.6.7.8:27015"})
	sv.ID = sid
	s.AddNotify("u", "g", sid, "ze_x")   // exact, this server
	s.AddNotify("u", "g", 0, "ze_x")     // exact, all servers
	s.AddNotify("u", "g", 0, "ze_*")     // wildcard: covers, never removed by Stop
	s.AddNotify("u", "g", other, "ze_x") // another server: not affected
	exact, covering := mapAlerts("u", sv, "ze_x")
	if len(exact) != 2 || len(covering) != 1 || covering[0].Pattern != "ze_*" {
		t.Errorf("exact = %+v\ncovering = %+v", exact, covering)
	}
}

func TestDeliveryAndErrors(t *testing.T) {
	coded := func(code int) error {
		return &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: code}}
	}
	for code, want := range map[int]bool{
		discordgo.ErrCodeUnknownChannel: true, discordgo.ErrCodeMissingAccess: true, discordgo.ErrCodeMissingPermissions: true,
		discordgo.ErrCodeCannotSendMessagesToThisUser: false, 0: false,
	} {
		if got := channelUnusable(coded(code)); got != want {
			t.Errorf("channelUnusable(%d) = %v", code, got)
		}
	}
	if apiCode(fmt.Errorf("wrapped: %w", coded(50007))) != 50007 || apiCode(errors.New("plain")) != 0 {
		t.Error("apiCode should see through wrapping and ignore other errors")
	}

	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sid, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: "1.1.1.1:27015"})
	s.AddNotify("dm-user", "g", sid, "*")
	s.AddNotify("ch-user", "g", 0, "*")
	s.SetDelivery("ch-user", "channel")
	sv, _ := s.Server("g", sid)
	got := map[string]string{}
	list, _ := s.NotifiesFor(*sv)
	for _, n := range list {
		got[n.UserID] = n.Delivery
	}
	if got["dm-user"] != "dm" || got["ch-user"] != "channel" {
		t.Errorf("delivery loaded with alerts = %v", got)
	}

	// Pausing and resuming: picking a channel resumes.
	s.SetPaused(sid, true)
	if sv, _ := s.Server("g", sid); !sv.Paused {
		t.Fatal("SetPaused didn't stick")
	}
	s.SetChannel("g", sid, "c2")
	if sv, _ := s.Server("g", sid); sv.Paused || sv.ChannelID != "c2" {
		t.Errorf("SetChannel should resume posts: %+v", sv)
	}
}

func TestConsoleHelpers(t *testing.T) {
	// Color codes don't count toward a panel line's width.
	if n := visibleLen("\x1b[38;5;78m● Online\x1b[0m"); n != 8 {
		t.Errorf("visibleLen = %d, want 8", n)
	}
	if got := wrap("one two three four", 9); strings.Join(got, "|") != "one two|three|four" {
		t.Errorf("wrap = %q", got)
	}
}

func TestRemoveAllDeletesUserData(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.AddNotify("u", "g1", 0, "ze_a")
	s.AddNotify("u", "g2", 0, "ze_b")
	s.SetDelivery("u", "channel")

	// Alerts left in another guild: the shared choice stays.
	s.RemoveAllNotifies("u", "g1")
	if s.Delivery("u") != "channel" {
		t.Fatal("the choice must stay while the user has alerts elsewhere")
	}
	// Last alerts gone: nothing about the user is left.
	s.RemoveAllNotifies("u", "g2")
	var rows int
	s.QueryRow("SELECT COUNT(*) FROM user_prefs WHERE user_id = 'u'").Scan(&rows)
	if rows != 0 {
		t.Error("the saved choice should be deleted with the last alert")
	}
	var alerts int
	s.QueryRow("SELECT COUNT(*) FROM notifications WHERE user_id = 'u'").Scan(&alerts)
	if alerts != 0 {
		t.Error("alerts left behind")
	}
}

func TestOnCooldown(t *testing.T) {
	if onCooldown("cooldown-user") {
		t.Fatal("first lookup should be allowed")
	}
	if !onCooldown("cooldown-user") {
		t.Fatal("second lookup right away should be blocked")
	}
	if onCooldown("other-user") {
		t.Fatal("cooldown is per user")
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token.txt")
	os.WriteFile(file, []byte("  secret-token\n"), 0o600)
	t.Setenv("SHIRON_CONF", filepath.Join(dir, "none.conf")) // ignore the developer's own shiron.conf
	t.Setenv("SHIRON_TOKEN", "")
	t.Setenv("DISCORD_TOKEN", "host-token")
	if c, _ := loadConfig(); c.Token != "host-token" {
		t.Fatalf("DISCORD_TOKEN should be used when SHIRON_TOKEN is empty, got %q", c.Token)
	}
	t.Setenv("DISCORD_TOKEN", "")
	t.Setenv("SHIRON_TOKEN_FILE", file)
	c, err := loadConfig()
	if err != nil || c.Token != "secret-token" {
		t.Fatalf("token file: %q, %v", c.Token, err)
	}
	if c.MapImages != defaultMapImages || c.ConnectURL != "" {
		t.Fatalf("defaults: %+v", c)
	}

	t.Setenv("SHIRON_TOKEN_FILE", filepath.Join(dir, "missing.txt"))
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing token file should fail")
	}

	t.Setenv("SHIRON_TOKEN", "env-token")
	for env, bad := range map[string]string{
		"SHIRON_CONNECT_URL": "http://not-https.example/connect.html",
		"SHIRON_MAP_IMAGES":  "https://img.example/no-placeholder.png",
		"SHIRON_POLL":        "1s",
	} {
		t.Setenv(env, bad)
		if _, err := loadConfig(); err == nil {
			t.Errorf("%s=%q should be rejected", env, bad)
		}
		t.Setenv(env, "")
	}
	t.Setenv("SHIRON_MAP_IMAGES", "none")
	if c, err := loadConfig(); err != nil || c.MapImages != "" || c.Token != "env-token" {
		t.Fatalf("none/env token: %+v, %v", c, err)
	}
}

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
		"169.254.169.254", "0.0.0.0", "0.1.2.3", "198.18.0.1", "240.0.0.1", "255.255.255.255", "[::ffff:127.0.0.1]:27015",
		"[64:ff9b::a00:1]:27015", "[64:ff9b:1::1]:27015", // NAT64 wrapping 10.0.0.1 and friends
		"::1", "[::1]:27015", "1.1.1.1:0", "1.1.1.1:99999", "1.1.1.1:abc", ""} {
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
	id2, err := s.AddServer(Server{GuildID: "g2", ChannelID: "c9", Address: "1.1.1.1:27015"})
	if err != nil {
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

	if err := s.AddNotify("u1", "g1", id, "de_*"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNotify("u1", "g1", id, "de_*"); !isUnique(err) {
		t.Fatalf("duplicate notify: want UNIQUE error, got %v", err)
	}
	if n, _ := s.Notifies("u1", "g1"); len(n) != 1 || n[0].Server.Address != "1.1.1.1:27015" || n[0].AllServers() {
		t.Fatalf("Notifies = %+v", n)
	}
	sv1, _ := s.Server("g1", id)
	if n, _ := s.NotifiesFor(*sv1); len(n) != 1 {
		t.Fatalf("NotifiesFor = %d", len(n))
	}

	// An all-servers alert applies to every server in its guild, and nowhere else.
	if err := s.AddNotify("u2", "g1", 0, "ze_toggle_theory"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddNotify("u2", "g1", 0, "ze_toggle_theory"); !isUnique(err) {
		t.Fatalf("duplicate all-servers alert: want UNIQUE error, got %v", err)
	}
	if n, _ := s.NotifiesFor(*sv1); len(n) != 2 {
		t.Fatalf("NotifiesFor with an all-servers alert = %d, want 2", len(n))
	}
	if n, _ := s.Notifies("u2", "g1"); len(n) != 1 || !n[0].AllServers() || n[0].Server.GuildID != "g1" {
		t.Fatalf("all-servers alert = %+v", n)
	}
	other, _ := s.Server("g2", id2)
	if n, _ := s.NotifiesFor(*other); len(n) != 0 {
		t.Fatalf("another guild's server got %d alerts", len(n))
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
	if n, _ := s.Notifies("u1", "g1"); len(n) != 0 {
		t.Fatal("notifications not cascaded")
	}
	if n, _ := s.Notifies("u2", "g1"); len(n) != 1 {
		t.Fatal("removing one server must keep all-servers alerts")
	}
	if err := s.DeleteGuild("g1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Notifies("u2", "g1"); len(n) != 0 {
		t.Fatal("DeleteGuild should remove all-servers alerts too")
	}
	if err := s.DeleteGuild("g2"); err != nil {
		t.Fatal(err)
	}
	if all, _ := s.Servers(""); len(all) != 0 {
		t.Fatalf("DeleteGuild left %d servers", len(all))
	}
	var prefs int
	s.QueryRow("SELECT COUNT(*) FROM user_prefs").Scan(&prefs)
	if prefs != 0 {
		t.Fatalf("DeleteGuild left %d saved DM/channel choices for users with no alerts", prefs)
	}
}
