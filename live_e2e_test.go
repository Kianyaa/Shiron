//go:build live

// End-to-end test of every button and admin action with simulated Discord clicks
// against real CS2 servers. Nothing is sent to Discord: a fake HTTP transport records
// what Shiron would send and every payload is checked against Discord's limits.
//
// Run with: go test -tags live -run TestE2E -v
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

const e2eServer = "103.62.49.55:27015" // [JP] Zombie Escape :: POSSESSION

type sent struct {
	Method, Path string
	Body         map[string]any
}

type recorder struct {
	mu   sync.Mutex
	reqs []sent
	fail map[string]int // path -> Discord error code to answer with (403)
}

func (r *recorder) failPath(path string, code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail == nil {
		r.fail = map[string]int{}
	}
	r.fail[path] = code
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		json.Unmarshal(b, &body)
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, sent{req.Method, req.URL.Path, body})
	code, failing := r.fail[req.URL.Path]
	r.mu.Unlock()
	if failing {
		msg := fmt.Sprintf(`{"code":%d,"message":"refused by the test"}`, code)
		return &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(bytes.NewBufferString(msg)), Request: req}, nil
	}
	resp := `{"id":"1","channel_id":"dm","type":1}`
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(bytes.NewBufferString(resp)), Request: req}, nil
}

func (r *recorder) take() []sent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.reqs
	r.reqs = nil
	return out
}

func newFakeSession(t *testing.T) (*discordgo.Session, *recorder) {
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	s.Client = &http.Client{Transport: rec}
	s.State.User = &discordgo.User{ID: "bot", Username: "ShironBot"}
	return s, rec
}

var userSeq int

// click builds the interaction Discord sends for a button press on msg.
func click(t *testing.T, kind int, data map[string]any, msg []discordgo.MessageComponent, admin bool) *discordgo.InteractionCreate {
	userSeq++ // a new user each time, so the 5s cooldown doesn't interfere
	perms := "0"
	if admin {
		perms = "32" // Manage Server
	}
	payload := map[string]any{
		"id": "i1", "application_id": "app", "type": kind, "token": "tok", "guild_id": "g", "channel_id": "c",
		"locale": "en-US", "guild_locale": "en-US", "data": data,
		"member": map[string]any{"user": map[string]any{"id": fmt.Sprintf("u%d", userSeq)}, "permissions": perms},
	}
	if msg != nil {
		payload["message"] = map[string]any{"id": "m1", "channel_id": "c", "components": msg}
	}
	raw, _ := json.Marshal(payload)
	var ic discordgo.InteractionCreate
	if err := json.Unmarshal(raw, &ic); err != nil {
		t.Fatalf("interaction json: %v", err)
	}
	return &ic
}

func press(id string) map[string]any { return map[string]any{"custom_id": id, "component_type": 2} }

// checkPayload enforces Discord's limits on a recorded request body.
func checkPayload(t *testing.T, where string, body map[string]any) {
	t.Helper()
	data := body
	if d, ok := body["data"].(map[string]any); ok {
		data = d
	}
	comps, _ := data["components"].([]any)
	flags, _ := data["flags"].(float64)
	if len(comps) > 0 && int(flags)&(1<<15) == 0 && data["title"] == nil {
		if hasV2(comps) {
			t.Errorf("%s: v2 components without the IS_COMPONENTS_V2 flag", where)
		}
	}
	if int(flags)&(1<<15) != 0 {
		if c, _ := data["content"].(string); c != "" {
			t.Errorf("%s: v2 message must not have content: %q", where, c)
		}
	}
	count, text := 0, 0
	ids := map[string]bool{}
	var walk func([]any)
	walk = func(list []any) {
		for _, x := range list {
			m, _ := x.(map[string]any)
			count++
			if s, ok := m["content"].(string); ok {
				text += len([]rune(s))
			}
			if id, ok := m["custom_id"].(string); ok {
				if len(id) > 100 {
					t.Errorf("%s: custom_id over 100: %q", where, id)
				}
				if ids[id] {
					t.Errorf("%s: duplicate custom_id %q", where, id)
				}
				ids[id] = true
			}
			if l, ok := m["label"].(string); ok && len([]rune(l)) > 80 {
				t.Errorf("%s: label over 80: %q", where, l)
			}
			if typ, _ := m["type"].(float64); typ == 1 {
				if inner, _ := m["components"].([]any); len(inner) > 5 {
					t.Errorf("%s: action row with %d items", where, len(inner))
				}
			}
			if inner, ok := m["components"].([]any); ok {
				walk(inner)
			}
			if acc, ok := m["accessory"].(map[string]any); ok {
				walk([]any{acc})
			}
		}
	}
	walk(comps)
	if count > 40 {
		t.Errorf("%s: %d components (max 40)", where, count)
	}
	if text > 4000 {
		t.Errorf("%s: %d characters of text (max 4000)", where, text)
	}
}

func hasV2(list []any) bool {
	for _, x := range list {
		m, _ := x.(map[string]any)
		switch typ, _ := m["type"].(float64); typ {
		case 9, 10, 11, 12, 13, 14, 17:
			return true
		}
	}
	return false
}

// allText joins every text display in a payload, for easy assertions.
func allText(body map[string]any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch v := x.(type) {
		case map[string]any:
			if s, ok := v["content"].(string); ok {
				b.WriteString(s + "\n")
			}
			if s, ok := v["custom_id"].(string); ok {
				b.WriteString("[id:" + s + "]\n")
			}
			if s, ok := v["label"].(string); ok {
				b.WriteString("[label:" + s + "]\n")
			}
			for _, k := range []string{"data", "components", "accessory"} {
				if inner, ok := v[k]; ok {
					walk(inner)
				}
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(body)
	return b.String()
}

func cardJSON(c discordgo.Container) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{c}
}

func TestE2E(t *testing.T) {
	s, err := openDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db = s
	cfg = Config{Poll: time.Minute, MapImages: defaultMapImages, LoadingEmoji: "⏳",
		ConnectURL: "https://kianyaa.github.io/shiron-join/connect.html"}

	live, err := queryInfo(e2eServer)
	if err != nil {
		t.Skipf("test server not answering: %v", err)
	}
	t.Logf("live server is on %s (%d/%d)", live.Map, live.Humans(), live.Max)
	id, _ := s.AddServer(Server{GuildID: "g", ChannelID: "c", Address: e2eServer, Name: live.Name, LastMap: live.Map})
	sv, _ := s.Server("g", id)
	sess, rec := newFakeSession(t)
	srvID := fmt.Sprint(id)

	run := func(name string, ic *discordgo.InteractionCreate) []sent {
		rec.take()
		onInteraction(sess, ic)
		reqs := rec.take()
		for i, r := range reqs {
			checkPayload(t, fmt.Sprintf("%s req %d %s %s", name, i, r.Method, r.Path), r.Body)
		}
		return reqs
	}

	t.Run("/map answers with a skeleton, then the live card", func(t *testing.T) {
		reqs := run("/map", click(t, 2, map[string]any{"id": "c", "name": "map", "type": 1}, nil, false))
		if len(reqs) != 2 {
			t.Fatalf("want callback + edit, got %d requests", len(reqs))
		}
		if !strings.Contains(allText(reqs[0].Body), "Checking live status") {
			t.Errorf("first answer should be the skeleton:\n%s", allText(reqs[0].Body))
		}
		if !strings.Contains(allText(reqs[1].Body), "## "+plain(live.Map)) {
			t.Errorf("edit should show the live map:\n%s", allText(reqs[1].Body))
		}
	})

	cur, _ := queryInfo(e2eServer) // refresh the live map right before the refresh tests
	mapCard := serverCard("en-US", *sv, cur, "")
	postCard := serverCard("en-US", *sv, cur, "ze_previous_map")
	oldCard := serverCard("en-US", *sv, &Status{Name: cur.Name, Map: "ze_long_gone_map", Players: 3, Max: 64}, "ze_before_that")

	t.Run("Refresh on a /map card, same map: updates in place", func(t *testing.T) {
		reqs := run("refresh same", click(t, 3, press("srv:refresh:"+srvID), cardJSON(mapCard), false))
		if len(reqs) != 2 || reqs[0].Body["type"].(float64) != 6 {
			t.Fatalf("want deferred update + edit, got %+v", reqs)
		}
		txt := allText(reqs[1].Body)
		if !strings.Contains(txt, "## "+plain(cur.Map)) || strings.Contains(txt, "Map changed from") {
			t.Errorf("unexpected edit:\n%s", txt)
		}
	})

	t.Run("Refresh on a map-change post, same map: keeps 'changed from'", func(t *testing.T) {
		reqs := run("refresh post", click(t, 3, press("srv:refresh:"+srvID), cardJSON(postCard), false))
		if len(reqs) != 2 {
			t.Fatalf("got %d requests", len(reqs))
		}
		txt := allText(reqs[1].Body)
		if !strings.Contains(txt, "Map changed from `ze_previous_map`") || !strings.Contains(txt, "## "+plain(cur.Map)) {
			t.Errorf("history line lost:\n%s", txt)
		}
	})

	for _, action := range []string{"refresh", "now"} {
		t.Run("Refresh ("+action+") after the map changed: card kept, note added", func(t *testing.T) {
			reqs := run("refresh old "+action, click(t, 3, press("srv:"+action+":"+srvID), cardJSON(oldCard), false))
			if len(reqs) != 2 {
				t.Fatalf("got %d requests: %+v", len(reqs), reqs)
			}
			txt := allText(reqs[1].Body)
			for _, want := range []string{"## ze\\_long\\_gone\\_map", "Map changed from `ze_before_that`", "Map has since changed to " + plain(cur.Map), "3 / 64"} {
				if !strings.Contains(txt, want) {
					t.Errorf("missing %q in kept card:\n%s", want, txt)
				}
			}
			if strings.Contains(txt, "srv:refresh:") {
				t.Errorf("Refresh should be gone from the outdated card")
			}
		})
	}

	t.Run("Refresh twice quickly: cooldown, card untouched", func(t *testing.T) {
		ic := click(t, 3, press("srv:refresh:"+srvID), cardJSON(mapCard), false)
		run("first", ic)
		reqs := run("second", ic) // same user
		if len(reqs) != 1 || !strings.Contains(allText(reqs[0].Body), "wait") || reqs[0].Method != "POST" {
			t.Errorf("want one private cooldown reply, got %+v", reqs)
		}
	})

	t.Run("Players on a current card: private scoreboard, card untouched", func(t *testing.T) {
		reqs := run("players", click(t, 3, press("srv:players:"+srvID), cardJSON(mapCard), false))
		if len(reqs) != 2 || reqs[0].Body["type"].(float64) != 6 || reqs[1].Method != "POST" {
			t.Fatalf("want deferred update + private follow-up, got %+v", reqs)
		}
		if f, _ := reqs[1].Body["flags"].(float64); int(f)&64 == 0 {
			t.Error("scoreboard should be private")
		}
		if txt := allText(reqs[1].Body); !strings.Contains(txt, "### Players ·") {
			t.Errorf("scoreboard missing:\n%s", txt)
		}
	})

	t.Run("Players on an old card: card kept as history, Players + Refresh gone", func(t *testing.T) {
		reqs := run("players old", click(t, 3, press("srv:players:"+srvID), cardJSON(oldCard), false))
		if len(reqs) != 2 || reqs[1].Method != "PATCH" {
			t.Fatalf("want deferred update + edit of the card, got %+v", reqs)
		}
		txt := allText(reqs[1].Body)
		if !strings.Contains(txt, "## ze\\_long\\_gone\\_map") || !strings.Contains(txt, "Map has since changed") {
			t.Errorf("card not kept as history:\n%s", txt)
		}
		if strings.Contains(txt, "srv:players:") || strings.Contains(txt, "srv:refresh:") {
			t.Error("Players and Refresh should be gone")
		}
		if !strings.Contains(txt, "srv:notify:") {
			t.Error("Notify should stay on the old card")
		}
	})

	t.Run("Notify me: add with Stop button, 'already' on second press, Stop removes it", func(t *testing.T) {
		stopID := "srv:unnotify:" + srvID + ":ze_long_gone_map"
		ic := click(t, 3, press("srv:notify:"+srvID), cardJSON(oldCard), false)
		reqs := run("notify on", ic)
		txt := allText(reqs[0].Body)
		if len(reqs) != 1 || !strings.Contains(txt, "`ze_long_gone_map`") || !strings.Contains(txt, "[id:"+stopID+"]") {
			t.Fatalf("add reply:\n%s", txt)
		}
		user := ic.Member.User.ID
		n, _ := s.Notifies(user, "g")
		if len(n) != 1 || n[0].Pattern != "ze_long_gone_map" || n[0].Server.ID != id {
			t.Fatalf("saved alert = %+v", n)
		}
		reqs = run("notify again", ic) // same user presses again: told, not toggled
		txt = allText(reqs[0].Body)
		if !strings.Contains(txt, "already get alerts") || !strings.Contains(txt, "[id:"+stopID+"]") {
			t.Fatalf("second press:\n%s", txt)
		}
		if n, _ := s.Notifies(user, "g"); len(n) != 1 {
			t.Fatal("a second press must not remove the alert")
		}
		// Stop on the private reply: the private message changes in place.
		stop := click(t, 3, press(stopID), nil, false)
		stop.Member.User.ID = user
		stop.Message = &discordgo.Message{Flags: discordgo.MessageFlagsEphemeral}
		reqs = run("stop", stop)
		if reqs[0].Body["type"].(float64) != 7 || !strings.Contains(allText(reqs[0].Body), "stopped") {
			t.Fatalf("stop reply: %+v", reqs)
		}
		if n, _ := s.Notifies(user, "g"); len(n) != 0 {
			t.Fatalf("alert should be gone, got %+v", n)
		}
		// A wildcard that covers the map is left alone and pointed to /notify list.
		s.AddNotify(user, "g", 0, "ze_long_*")
		reqs = run("stop wildcard", stop)
		if !strings.Contains(allText(reqs[0].Body), "`ze_long_*`") {
			t.Errorf("wildcard hint missing: %s", allText(reqs[0].Body))
		}
		if n, _ := s.Notifies(user, "g"); len(n) != 1 {
			t.Error("Stop must not delete a wildcard alert")
		}
	})

	t.Run("Copy IP: private connect command", func(t *testing.T) {
		reqs := run("ip", click(t, 3, press("srv:ip:"+srvID), cardJSON(mapCard), false))
		if len(reqs) != 1 || !strings.Contains(allText(reqs[0].Body), "connect "+e2eServer) {
			t.Errorf("got %+v", reqs)
		}
	})

	t.Run("More (compact): private skeleton, then the full card", func(t *testing.T) {
		reqs := run("more", click(t, 3, press("srv:more:"+srvID), cardJSON(mapCard), false))
		if len(reqs) != 2 || !strings.Contains(allText(reqs[1].Body), "## ") {
			t.Errorf("got %d requests:\n%s", len(reqs), allText(reqs[len(reqs)-1].Body))
		}
	})

	t.Run("Button for a removed server", func(t *testing.T) {
		reqs := run("gone", click(t, 3, press("srv:refresh:999999"), cardJSON(mapCard), false))
		if len(reqs) != 1 || !strings.Contains(allText(reqs[0].Body), "isn't tracked anymore") {
			t.Errorf("got %+v", reqs)
		}
	})

	// ---------- /servers list and the Manage panel ----------
	page := fmt.Sprintf("%s:0", srvID)
	t.Run("/servers list", func(t *testing.T) {
		reqs := run("list", click(t, 2, map[string]any{"id": "c", "name": "servers", "type": 1,
			"options": []any{map[string]any{"name": "list", "type": 1}}}, nil, true))
		txt := allText(reqs[0].Body)
		for _, want := range []string{"`#1`", "[label:Manage]", "[label:Map pictures]", "[label:Collections]"} {
			if !strings.Contains(txt, want) {
				t.Errorf("list missing %q:\n%s", want, txt)
			}
		}
	})
	t.Run("non-admin can't use Manage", func(t *testing.T) {
		reqs := run("not admin", click(t, 3, press("srvm:open:"+page), nil, false))
		if !strings.Contains(allText(reqs[0].Body), "Manage Server permission") {
			t.Errorf("got %s", allText(reqs[0].Body))
		}
	})
	t.Run("Manage opens the panel in place", func(t *testing.T) {
		reqs := run("open", click(t, 3, press("srvm:open:"+page), nil, true))
		if reqs[0].Body["type"].(float64) != 7 || !strings.Contains(allText(reqs[0].Body), "Switch to Compact") {
			t.Errorf("panel:\n%s", allText(reqs[0].Body))
		}
	})
	t.Run("Style toggles and saves", func(t *testing.T) {
		run("style", click(t, 3, press("srvm:style:"+page), nil, true))
		if got, _ := s.Server("g", id); got.Style != "compact" {
			t.Errorf("style = %q", got.Style)
		}
		run("style back", click(t, 3, press("srvm:style:"+page), nil, true))
		if got, _ := s.Server("g", id); got.Style != "full" {
			t.Errorf("style back = %q", got.Style)
		}
	})
	t.Run("Min players opens a form, saves valid input, rejects bad input", func(t *testing.T) {
		reqs := run("min", click(t, 3, press("srvm:min:"+page), nil, true))
		if reqs[0].Body["type"].(float64) != 9 {
			t.Errorf("want a modal, got %+v", reqs[0].Body)
		}
		form := func(v string) map[string]any {
			return map[string]any{"custom_id": "srvm:minset:" + page, "components": []any{map[string]any{"type": 1,
				"components": []any{map[string]any{"type": 4, "custom_id": "value", "value": v}}}}}
		}
		run("minset", click(t, 5, form("10"), nil, true))
		if got, _ := s.Server("g", id); got.MinPlayers != 10 {
			t.Errorf("min players = %d", got.MinPlayers)
		}
		reqs = run("minset bad", click(t, 5, form("abc"), nil, true))
		if !strings.Contains(allText(reqs[0].Body), "from 0 to 128") {
			t.Errorf("bad input not rejected: %s", allText(reqs[0].Body))
		}
		run("minset 0", click(t, 5, form("0"), nil, true))
	})
	t.Run("Channel picker saves", func(t *testing.T) {
		run("chan", click(t, 3, map[string]any{"custom_id": "srvm:chan:" + page, "component_type": 8, "values": []any{"c2"}}, nil, true))
		if got, _ := s.Server("g", id); got.ChannelID != "c2" {
			t.Errorf("channel = %q", got.ChannelID)
		}
		s.SetChannel("g", id, "c")
	})
	t.Run("Remove asks first; Cancel keeps it", func(t *testing.T) {
		reqs := run("rm", click(t, 3, press("srvm:rm:"+page), nil, true))
		if !strings.Contains(allText(reqs[0].Body), "Yes, remove") {
			t.Errorf("no confirmation:\n%s", allText(reqs[0].Body))
		}
		run("cancel", click(t, 3, press("srvm:open:"+page), nil, true))
		if _, err := s.Server("g", id); err != nil {
			t.Error("cancel must keep the server")
		}
	})

	// ---------- map-change posts and alerts ----------
	t.Run("Map change post with channel mentions and a DM", func(t *testing.T) {
		s.AddNotify("fan1", "g", id, "*")
		s.SetDelivery("fan1", "channel")
		s.AddNotify("fan2", "g", 0, "*") // all servers
		got, _ := s.Server("g", id)
		got.LastMap = "ze_previous_map"
		rec.take()
		announce(sess, got, cur)
		drainDMs(sess) // DMs are queued; send them now instead of spaced out
		reqs := rec.take()
		var post, dm bool
		for i, r := range reqs {
			checkPayload(t, fmt.Sprintf("announce req %d %s", i, r.Path), r.Body)
			txt := allText(r.Body)
			if r.Path == "/api/v9/channels/c/messages" && strings.Contains(txt, "<@fan1>") && strings.Contains(txt, "Map changed from `ze_previous_map`") {
				post = true
				if am, _ := r.Body["allowed_mentions"].(map[string]any); fmt.Sprint(am["users"]) != "[fan1]" {
					t.Errorf("only fan1 may be pinged, allowed_mentions = %v", am)
				}
			}
			t.Logf("announce request: %s %s", r.Method, r.Path)
			if r.Path == "/api/v9/channels/1/messages" { // the fake Discord gives the DM channel ID 1
				dm = true
				if !strings.Contains(txt, "srv:unnotify:") || strings.Contains(txt, "[id:srv:notify:") {
					t.Errorf("a DM alert should offer Stop alerts instead of Notify me:\n%s", txt)
				}
			}
			if r.Path == "/api/v9/channels/c/messages" && strings.Contains(txt, "unnotify") {
				t.Error("the channel post must keep Notify me, not one user's Stop button")
			}
		}
		if !post || !dm {
			t.Errorf("post with mention: %v, DM: %v (%d requests)", post, dm, len(reqs))
		}
	})

	t.Run("/notify add without a server: alert for all servers", func(t *testing.T) {
		ic := click(t, 2, map[string]any{"id": "c", "name": "notify", "type": 1, "options": []any{map[string]any{
			"name": "add", "type": 1, "options": []any{map[string]any{"name": "map", "type": 3, "value": "ze_toggle_theory"}}}}}, nil, false)
		reqs := run("notify add", ic)
		if len(reqs) != 1 || !strings.Contains(allText(reqs[0].Body), "all servers here") {
			t.Fatalf("reply: %+v", reqs)
		}
		user := ic.Member.User.ID
		n, _ := s.Notifies(user, "g")
		if len(n) != 1 || !n[0].AllServers() || n[0].Pattern != "ze_toggle_theory" {
			t.Fatalf("saved alert = %+v", n)
		}
		got, _ := s.Server("g", id)
		alerts, _ := s.NotifiesFor(*got)
		found := false
		for _, a := range alerts {
			found = found || a.UserID == user
		}
		if !found {
			t.Error("the all-servers alert must apply to this server")
		}
	})

	t.Run("/notify list: rows, switch delivery, remove, others can't remove", func(t *testing.T) {
		owner := "listowner"
		s.AddNotify(owner, "g", 0, "ze_list_a")
		s.AddNotify(owner, "g", id, "ze_list_b")
		n, _ := s.Notifies(owner, "g")
		as := func(user string, ic *discordgo.InteractionCreate) *discordgo.InteractionCreate {
			ic.Member.User.ID = user
			return ic
		}
		cmd := map[string]any{"id": "c", "name": "notify", "type": 1, "options": []any{map[string]any{"name": "list", "type": 1}}}
		reqs := run("list", as(owner, click(t, 2, cmd, nil, false)))
		txt := allText(reqs[0].Body)
		for _, want := range []string{"Your map alerts · 2", "`#1` `ze_list_a`", "all servers here", "`#2` `ze_list_b`", "[label:Edit]", "[label:Remove all]", "Send by mention"} {
			if !strings.Contains(txt, want) {
				t.Errorf("list missing %q:\n%s", want, txt)
			}
		}
		reqs = run("switch", as(owner, click(t, 3, press("ntf:mode:0:0"), nil, false)))
		if s.Delivery(owner) != "channel" || reqs[0].Body["type"].(float64) != 7 || !strings.Contains(allText(reqs[0].Body), "Send by DM instead") {
			t.Errorf("switch: delivery=%s\n%s", s.Delivery(owner), allText(reqs[0].Body))
		}
		rm := fmt.Sprintf("ntf:rm:%d:0", n[0].ID)
		run("stranger rm", as("stranger", click(t, 3, press(rm), nil, false)))
		if left, _ := s.Notifies(owner, "g"); len(left) != 2 {
			t.Fatal("a stranger removed someone else's alert")
		}
		reqs = run("rm", as(owner, click(t, 3, press(rm), nil, false)))
		left, _ := s.Notifies(owner, "g")
		if len(left) != 1 || left[0].Pattern != "ze_list_b" || !strings.Contains(allText(reqs[0].Body), "Your map alerts · 1") {
			t.Errorf("after remove: %+v\n%s", left, allText(reqs[0].Body))
		}

		// Edit: the panel, then "where" (one server -> all servers), then change map.
		b := left[0]
		open := fmt.Sprintf("ntf:open:%d:0", b.ID)
		reqs = run("edit open", as(owner, click(t, 3, press(open), nil, false)))
		if txt := allText(reqs[0].Body); !strings.Contains(txt, "[label:Change map]") || !strings.Contains(txt, fmt.Sprintf("[id:ntf:where:%d:0]", b.ID)) {
			t.Errorf("edit panel:\n%s", txt)
		}
		where := map[string]any{"custom_id": fmt.Sprintf("ntf:where:%d:0", b.ID), "component_type": 3, "values": []any{"0"}}
		run("stranger where", as("stranger", click(t, 3, where, nil, false)))
		if got, _ := s.Notifies(owner, "g"); got[0].AllServers() {
			t.Fatal("a stranger changed someone else's alert")
		}
		reqs = run("where all", as(owner, click(t, 3, where, nil, false)))
		if got, _ := s.Notifies(owner, "g"); !got[0].AllServers() || !strings.Contains(allText(reqs[0].Body), "all servers here") {
			t.Errorf("where -> all servers: %+v", got)
		}
		where["values"] = []any{"999999"} // not a server in this guild: ignored
		run("where bogus", as(owner, click(t, 3, where, nil, false)))
		if got, _ := s.Notifies(owner, "g"); !got[0].AllServers() {
			t.Error("a server from elsewhere must be refused")
		}
		mapForm := func(v string) map[string]any {
			return map[string]any{"custom_id": fmt.Sprintf("ntf:mapset:%d:0", b.ID), "components": []any{map[string]any{"type": 1,
				"components": []any{map[string]any{"type": 4, "custom_id": "map", "value": v}}}}}
		}
		run("map form", as(owner, click(t, 3, press(fmt.Sprintf("ntf:map:%d:0", b.ID)), nil, false)))
		reqs = run("mapset", as(owner, click(t, 5, mapForm("ZE_New_*"), nil, false)))
		if got, _ := s.Notifies(owner, "g"); got[0].Pattern != "ze_new_*" || !strings.Contains(allText(reqs[0].Body), "`ze_new_*`") {
			t.Errorf("change map: %+v", got)
		}
		reqs = run("mapset bad", as(owner, click(t, 5, mapForm("ze_[bad"), nil, false)))
		if !strings.Contains(allText(reqs[0].Body), "isn't valid") {
			t.Errorf("bad pattern accepted: %s", allText(reqs[0].Body))
		}
		s.AddNotify(owner, "g", 0, "ze_dupe")
		reqs = run("mapset dupe", as(owner, click(t, 5, mapForm("ze_dupe"), nil, false)))
		if !strings.Contains(allText(reqs[0].Body), "already have an alert") {
			t.Errorf("duplicate accepted: %s", allText(reqs[0].Body))
		}

		// Remove all asks first; Cancel keeps everything; Yes removes only this user's alerts.
		s.AddNotify("bystander", "g", 0, "ze_theirs")
		reqs = run("rmall", as(owner, click(t, 3, press("ntf:rmall:0:0"), nil, false)))
		if !strings.Contains(allText(reqs[0].Body), "Remove all 2 of your map alerts") {
			t.Errorf("no confirmation: %s", allText(reqs[0].Body))
		}
		run("rmall cancel", as(owner, click(t, 3, press("ntf:page:0:0"), nil, false)))
		if got, _ := s.Notifies(owner, "g"); len(got) != 2 {
			t.Error("cancel must keep the alerts")
		}
		run("rmall yes", as(owner, click(t, 3, press("ntf:rmallyes:0:0"), nil, false)))
		if got, _ := s.Notifies(owner, "g"); len(got) != 0 {
			t.Errorf("remove all left %d", len(got))
		}
		if got, _ := s.Notifies("bystander", "g"); len(got) != 1 {
			t.Error("remove all must not touch other users' alerts")
		}
		var prefs int
		s.QueryRow("SELECT COUNT(*) FROM user_prefs WHERE user_id = ?", owner).Scan(&prefs)
		if prefs != 0 {
			t.Error("remove all should also delete the saved DM/channel choice (owner switched to channel earlier)")
		}
	})

	t.Run("/servers add ip: adds a CS2 server, refuses private IPs and duplicates", func(t *testing.T) {
		add := func(ip string) string {
			cmd := map[string]any{"id": "c", "name": "servers", "type": 1, "options": []any{map[string]any{
				"name": "add", "type": 1, "options": []any{map[string]any{"name": "ip", "type": 3, "value": ip}}}}}
			reqs := run("servers add "+ip, click(t, 2, cmd, nil, true))
			return allText(reqs[len(reqs)-1].Body)
		}
		if txt := add("74.91.124.21:27015"); !strings.Contains(txt, "Now tracking") { // GFL Zombie Escape
			t.Errorf("add: %s", txt)
		}
		if txt := add("74.91.124.21:27015"); !strings.Contains(txt, "already tracked") {
			t.Errorf("duplicate: %s", txt)
		}
		if txt := add("127.0.0.1"); !strings.Contains(txt, "isn't valid") {
			t.Errorf("private IP: %s", txt)
		}
		list, _ := s.Servers("g")
		for _, sv := range list {
			if sv.Address == "74.91.124.21:27015" {
				s.RemoveServer("g", sv.ID)
			}
		}
	})

	t.Run("/players: live card with the scoreboard", func(t *testing.T) {
		cmd := map[string]any{"id": "c", "name": "players", "type": 1}
		reqs := run("players cmd", click(t, 2, cmd, nil, false))
		if len(reqs) != 2 {
			t.Fatalf("got %d requests", len(reqs))
		}
		txt := allText(reqs[1].Body)
		if !strings.Contains(txt, "## ") || !(strings.Contains(txt, "PLAYER") || strings.Contains(txt, "Nobody is playing") || strings.Contains(txt, "doesn't share")) {
			t.Errorf("players card:\n%s", txt)
		}
	})

	form := func(customID string, fields map[string]string) map[string]any {
		var rows []any
		for k, v := range fields {
			rows = append(rows, map[string]any{"type": 1, "components": []any{map[string]any{"type": 4, "custom_id": k, "value": v}}})
		}
		return map[string]any{"custom_id": customID, "components": rows}
	}

	t.Run("Map pictures form: set by Workshop link, reject bad link, remove", func(t *testing.T) {
		reqs := run("pic", click(t, 3, press("srvm:pic:0:0"), nil, true))
		if reqs[0].Body["type"].(float64) != 9 {
			t.Fatalf("want a form, got %+v", reqs[0].Body)
		}
		reqs = run("picset", click(t, 5, form("srvm:picset:0:0", map[string]string{
			"map": "zm_420_office_p", "link": "https://steamcommunity.com/sharedfiles/filedetails/?id=3164948364"}), nil, true))
		if len(reqs) != 2 || reqs[0].Body["type"].(float64) != 6 || !strings.Contains(allText(reqs[1].Body), "✅") {
			t.Fatalf("set: %+v", reqs)
		}
		if u := s.MapPicture("g", "zm_420_office_p"); !strings.HasPrefix(u, "https://images.steamusercontent.com/") {
			t.Errorf("saved picture = %q", u)
		}
		reqs = run("picset bad", click(t, 5, form("srvm:picset:0:0", map[string]string{"map": "zm_x", "link": "not a link"}), nil, true))
		if !strings.Contains(allText(reqs[len(reqs)-1].Body), "❌") || s.MapPicture("g", "zm_x") != "" {
			t.Error("a bad link must be refused")
		}
		run("picset clear", click(t, 5, form("srvm:picset:0:0", map[string]string{"map": "zm_420_office_p", "link": ""}), nil, true))
		if s.MapPicture("g", "zm_420_office_p") != "" {
			t.Error("an empty link should remove the picture")
		}
		reqs = run("pic not admin", click(t, 3, press("srvm:pic:0:0"), nil, false))
		if !strings.Contains(allText(reqs[0].Body), "Manage Server permission") {
			t.Error("non-admins must not open the form")
		}
	})

	t.Run("Collections form: link POSSESSION's collection, then clear", func(t *testing.T) {
		reqs := run("col", click(t, 3, press("srvm:col:0:0"), nil, true))
		if reqs[0].Body["type"].(float64) != 9 {
			t.Fatalf("want a form, got %+v", reqs[0].Body)
		}
		reqs = run("colset", click(t, 5, form("srvm:colset:0:0", map[string]string{
			"link": "https://steamcommunity.com/sharedfiles/filedetails/?id=3667545564"}), nil, true))
		if txt := allText(reqs[len(reqs)-1].Body); !strings.Contains(txt, "✅") || len(s.Collections("g")) != 1 {
			t.Fatalf("link: %s", txt)
		}
		run("colset clear", click(t, 5, form("srvm:colset:0:0", map[string]string{"link": ""}), nil, true))
		if len(s.Collections("g")) != 0 {
			t.Error("an empty link should clear the collections")
		}
	})

	t.Run("/about and its Full changelog button", func(t *testing.T) {
		reqs := run("about", click(t, 2, map[string]any{"id": "c", "name": "about", "type": 1}, nil, false))
		txt := allText(reqs[0].Body)
		for _, want := range []string{"## ShironBot", "v1.0.2", "What's new in v1.0.2", "CS2 servers tracked", "[label:Invite Shiron]", "[id:about:log]"} {
			if !strings.Contains(txt, want) {
				t.Errorf("/about missing %q:\n%s", want, txt)
			}
		}
		reqs = run("changelog", click(t, 3, press("about:log"), nil, false))
		if f, _ := reqs[0].Body["data"].(map[string]any)["flags"].(float64); int(f)&64 == 0 || !strings.Contains(allText(reqs[0].Body), "**v1.0.2**") {
			t.Errorf("changelog should be private and list v1.0.2: %s", allText(reqs[0].Body))
		}
	})

	t.Run("/servers commands need Manage Server even if a role was granted access", func(t *testing.T) {
		cmd := map[string]any{"id": "c", "name": "servers", "type": 1, "options": []any{map[string]any{"name": "list", "type": 1}}}
		reqs := run("list not admin", click(t, 2, cmd, nil, false))
		if !strings.Contains(allText(reqs[0].Body), "Manage Server permission") {
			t.Errorf("non-admin got: %s", allText(reqs[0].Body))
		}
	})

	t.Run("Closed DMs: after 2 failures the user is switched to channel mentions", func(t *testing.T) {
		s.RemoveAllNotifies("fan1", "g")
		s.RemoveAllNotifies("fan2", "g")
		s.AddNotify("closed", "g", 0, "*")
		s.SetDelivery("closed", "dm")
		rec.failPath("/api/v9/channels/1/messages", discordgo.ErrCodeCannotSendMessagesToThisUser)
		got, _ := s.Server("g", id)
		got.LastMap = "ze_before"
		for i := 0; i < 2; i++ {
			announce(sess, got, cur)
			drainDMs(sess)
		}
		if d := s.Delivery("closed"); d != "channel" {
			t.Fatalf("delivery after 2 closed-DM failures = %q, want channel", d)
		}
		rec.take()
		announce(sess, got, cur)
		drainDMs(sess)
		dmTries, pinged := 0, false
		for _, r := range rec.take() {
			if r.Path == "/api/v9/channels/1/messages" {
				dmTries++
			}
			if r.Path == "/api/v9/channels/c/messages" && strings.Contains(allText(r.Body), "<@closed>") {
				pinged = true
			}
		}
		if dmTries != 0 || !pinged {
			t.Errorf("after switching: %d DM tries (want 0), pinged in channel: %v", dmTries, pinged)
		}
		rec.failPath("/api/v9/channels/1/messages", 0)
		delete(rec.fail, "/api/v9/channels/1/messages")
		s.RemoveAllNotifies("closed", "g")
	})

	t.Run("Lost channel: posts pause after one failure, resume when a channel is picked", func(t *testing.T) {
		rec.failPath("/api/v9/channels/c/messages", discordgo.ErrCodeUnknownChannel)
		got, _ := s.Server("g", id)
		got.LastMap = "ze_before"
		rec.take()
		announce(sess, got, cur)
		postStatus(sess, got, "offline", time.Time{})
		tries := 0
		for _, r := range rec.take() {
			if r.Path == "/api/v9/channels/c/messages" {
				tries++
			}
		}
		paused, _ := s.Server("g", id)
		if tries != 1 || !paused.Paused {
			t.Fatalf("channel tries = %d (want 1), paused = %v", tries, paused.Paused)
		}
		// Next round: nothing is sent to the dead channel at all.
		announce(sess, paused, cur)
		postStatus(sess, paused, "online", time.Time{})
		for _, r := range rec.take() {
			if r.Path == "/api/v9/channels/c/messages" {
				t.Fatal("a paused server must not post")
			}
		}
		// The admin sees it in the list, and picking a channel resumes.
		txt := allText(run("list paused", click(t, 3, press("srvm:page:0:0"), nil, true))[0].Body)
		if !strings.Contains(txt, "Posts paused") {
			t.Errorf("list should show the pause:\n%s", txt)
		}
		delete(rec.fail, "/api/v9/channels/c/messages")
		run("pick channel", click(t, 3, map[string]any{"custom_id": fmt.Sprintf("srvm:chan:%d:0", id), "component_type": 8, "values": []any{"c"}}, nil, true))
		if again, _ := s.Server("g", id); again.Paused {
			t.Error("picking a channel should resume posts")
		}
	})

	t.Run("Remove for real", func(t *testing.T) {
		run("rmyes", click(t, 3, press("srvm:rmyes:"+page), nil, true))
		if _, err := s.Server("g", id); err == nil {
			t.Error("server should be removed")
		}
	})
}
