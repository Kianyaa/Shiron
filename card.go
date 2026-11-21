package main

// card.go: the server card (Discord "components v2" layout) and its buttons.

import (
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	"github.com/rumblefrog/go-a2s"
)

const (
	colorBusy  = 0x57F287 // at least half full
	colorQuiet = 0xFEE75C // someone is playing
	colorEmpty = 0x80848E
)

// accent colors the card's side stripe by how busy the server is.
func accent(st *Status) int {
	switch {
	case st.Max > 0 && st.Humans()*2 >= st.Max:
		return colorBusy
	case st.Humans() > 0:
		return colorQuiet
	}
	return colorEmpty
}

// playerBar draws how full a server is in n colored segments, matching the card's
// stripe color; any player shows at least one segment.
func playerBar(st *Status, n int) string {
	filled := 0
	if st.Max > 0 {
		filled = min((st.Humans()*n+st.Max-1)/st.Max, n)
	}
	seg := "🟨"
	if accent(st) == colorBusy {
		seg = "🟩"
	}
	return strings.Repeat(seg, filled) + strings.Repeat("⬛", n-filled)
}

var plainEscaper = strings.NewReplacer(
	`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "`", "\\`", "|", `\|`, "[", `\[`, "]", `\]`,
	"(", `\(`, ")", `\)`, "<", `\<`, ">", `\>`, "#", `\#`,
	"@", "@​", // no mention can form
	"://", ":​//", // no autolink can form
	"\n", " ", "\r", " ",
)

// plain shows untrusted text (a server name) as normal text in markdown, with no
// formatting, links or mentions able to form.
func plain(s string) string { return plainEscaper.Replace(truncate(strings.TrimSpace(s), 150)) }

// serverCard is the card for one server, in the style its admins chose. prevMap, when
// set, adds a "map changed" line.
func serverCard(loc string, sv Server, st *Status, prevMap string) discordgo.Container {
	if sv.Style == "compact" {
		return compactCard(loc, sv, st, prevMap)
	}
	return fullCard(loc, sv, st, prevMap)
}

// compactCard is a short card: text with a small map thumbnail, Join (or Copy IP) and
// a More button that privately shows the full card.
func compactCard(loc string, sv Server, st *Status, prevMap string) discordgo.Container {
	var t strings.Builder
	if prevMap != "" {
		t.WriteString("-# " + T(loc, "card.changed", code(prevMap)) + "\n")
	}
	// <t:…:R> is counted live by each Discord app ("29 seconds ago"), so it costs nothing.
	now := time.Now().Unix()
	fmt.Fprintf(&t, "### %s\n-# %s\n%s  **%d / %d**%s\n-# %s",
		plain(st.Map), plain(truncate(st.Name, 60)), playerBar(st, 5), st.Humans(), st.Max, mapStarted(loc, st), T(loc, "card.updated", now))
	var top discordgo.MessageComponent = discordgo.TextDisplay{Content: t.String()}
	if img := mapPicture(sv.GuildID, st.Map); img != "" {
		top = discordgo.Section{Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: t.String()}},
			Accessory: discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: img}}}
	}
	b := joinButton(loc, sv)
	if len(b) == 0 {
		b = append(b, button(loc, "button.copy_ip", "📋", "ip", sv))
	}
	b = append(b, button(loc, "button.more", "", "more", sv), button(loc, "button.notify", "🔔", "notify", sv))
	return discordgo.Container{AccentColor: ptr(accent(st)), Components: []discordgo.MessageComponent{top, discordgo.ActionsRow{Components: b}}}
}

// fullCard is the big card: map picture, headline, player bar and all buttons.
func fullCard(loc string, sv Server, st *Status, prevMap string) discordgo.Container {
	var parts []discordgo.MessageComponent
	if img := mapPicture(sv.GuildID, st.Map); img != "" {
		parts = append(parts, discordgo.MediaGallery{Items: []discordgo.MediaGalleryItem{{Media: discordgo.UnfurledMediaItem{URL: img}}}})
	}
	var head strings.Builder
	if prevMap != "" {
		head.WriteString("-# " + T(loc, "card.changed", code(prevMap)) + "\n")
	}
	// plain() escapes "_" so map names don't turn into italics in the headline.
	head.WriteString("## " + plain(st.Map) + "\n" + plain(st.Name))
	parts = append(parts,
		discordgo.TextDisplay{Content: head.String()},
		discordgo.TextDisplay{Content: fmt.Sprintf("%s  **%d / %d**%s\n-# %s",
			playerBar(st, 10), st.Humans(), st.Max, mapStarted(loc, st), T(loc, "card.footer", st.Bots, time.Now().Unix()))},
		discordgo.ActionsRow{Components: cardButtons(loc, sv)},
	)
	return discordgo.Container{AccentColor: ptr(accent(st)), Components: parts}
}

// mapStarted is the line saying when the current map started, or "" when unknown.
// Each time sits next to its own label, so it isn't mixed up with the "updated" time.
func mapStarted(loc string, st *Status) string {
	if st.MapSince == 0 {
		return ""
	}
	return "\n-# " + T(loc, "card.map_started", st.MapSince)
}

// skeletonCard is sent instantly while a server is being queried, instead of Discord's
// "is thinking…": what Shiron already knows (name, last map, a cached picture), a
// loading line, and the buttons greyed out. It does no network work.
func skeletonCard(loc string, sv Server) discordgo.Container {
	var parts []discordgo.MessageComponent
	if img := cachedMapPicture(sv.GuildID, sv.LastMap); img != "" {
		parts = append(parts, discordgo.MediaGallery{Items: []discordgo.MediaGalleryItem{{Media: discordgo.UnfurledMediaItem{URL: img}}}})
	}
	head := "## " + plain(sv.LastMap)
	if sv.LastMap == "" {
		head = "## …"
	}
	if sv.Name != "" {
		head += "\n-# " + plain(sv.Name)
	}
	status := cfg.LoadingEmoji + " " + T(loc, "card.checking")
	if t, ok := lastSeen.Load(sv.Address); ok {
		status += " · " + T(loc, "card.last_seen", t.(time.Time).Unix())
	}
	buttons := cardButtons(loc, sv)
	for i, b := range buttons {
		if btn, ok := b.(discordgo.Button); ok {
			btn.Disabled = true
			buttons[i] = btn
		}
	}
	return discordgo.Container{AccentColor: ptr(colorEmpty), Components: append(parts,
		discordgo.TextDisplay{Content: head},
		discordgo.TextDisplay{Content: status},
		discordgo.ActionsRow{Components: buttons},
	)}
}

// withText inserts extra text (like a player list) just above the card's buttons.
func withText(card discordgo.Container, text string) discordgo.Container {
	n := len(card.Components)
	card.Components = append(card.Components[:n-1:n-1], discordgo.TextDisplay{Content: text}, card.Components[n-1])
	return card
}

// cardButtons are a card's buttons. Refresh always edits the card it's on, and only
// while the server is still on the card's map (see onButton), so history is safe.
func cardButtons(loc string, sv Server) []discordgo.MessageComponent {
	return append(joinButton(loc, sv),
		button(loc, "button.copy_ip", "📋", "ip", sv),
		button(loc, "button.players", "👥", "players", sv),
		button(loc, "button.refresh", "🔄", "refresh", sv),
		button(loc, "button.notify", "🔔", "notify", sv),
	)
}

// liveOnly are the buttons that only make sense while the server is still on the
// card's map; markOutdated removes them ("now" is Refresh from an earlier build).
var liveOnly = []string{"srv:refresh:", "srv:now:", "srv:players:"}

// cardPrevMap reads the "Map changed from `x`" line of a card, so a refresh keeps it.
func cardPrevMap(comps []discordgo.MessageComponent) string {
	for _, comp := range comps {
		box, ok := comp.(*discordgo.Container)
		if !ok {
			continue
		}
		for _, c := range box.Components {
			td, ok := c.(*discordgo.TextDisplay)
			if !ok {
				continue
			}
			for _, line := range strings.Split(td.Content, "\n") {
				if !strings.HasPrefix(line, "-# ") {
					continue
				}
				if parts := strings.Split(line, "`"); len(parts) >= 3 {
					return parts[1]
				}
			}
		}
	}
	return ""
}

// cardMap reads the map a card shows from its headline ("## ze\_x" or "### ze\_x"),
// so Refresh knows what the card is about, on cards from any build. "" if not found.
func cardMap(comps []discordgo.MessageComponent) string {
	for _, comp := range comps {
		box, ok := comp.(*discordgo.Container)
		if !ok {
			continue
		}
		var parts []discordgo.MessageComponent
		for _, c := range box.Components {
			if sec, ok := c.(*discordgo.Section); ok {
				parts = append(parts, sec.Components...)
			} else {
				parts = append(parts, c)
			}
		}
		for _, p := range parts {
			td, ok := p.(*discordgo.TextDisplay)
			if !ok {
				continue
			}
			for _, line := range strings.Split(td.Content, "\n") {
				if h, found := strings.CutPrefix(line, "## "); found {
					return strings.ReplaceAll(h, `\`, "")
				}
				if h, found := strings.CutPrefix(line, "### "); found {
					return strings.ReplaceAll(h, `\`, "")
				}
			}
		}
	}
	return ""
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// markOutdated keeps a card whose server has moved to another map: it adds a note
// with the new map and drops the Refresh button, editing nothing else. ok is false
// when the card isn't shaped as expected (then the caller leaves it alone).
func markOutdated(comps []discordgo.MessageComponent, note string) (out []discordgo.MessageComponent, ok bool) {
	for _, comp := range comps {
		box, isBox := comp.(*discordgo.Container)
		if !isBox || len(box.Components) == 0 {
			continue
		}
		row, isRow := box.Components[len(box.Components)-1].(*discordgo.ActionsRow)
		if !isRow {
			return nil, false
		}
		var kept []discordgo.MessageComponent
		for _, b := range row.Components {
			if btn, isBtn := b.(*discordgo.Button); isBtn && hasAnyPrefix(btn.CustomID, liveOnly) {
				continue
			}
			kept = append(kept, b)
		}
		row.Components = kept
		n := len(box.Components)
		box.Components = append(box.Components[:n-1:n-1], textV2(note), row)
		return comps, true
	}
	return nil, false
}

// joinButton is the Join server link, or nothing when no connect page is set.
// Discord only allows http(s) links on buttons, so this goes through the connect page.
func joinButton(loc string, sv Server) []discordgo.MessageComponent {
	if cfg.ConnectURL == "" {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.Button{Label: T(loc, "button.join"), Style: discordgo.LinkButton,
		URL: cfg.ConnectURL + "#" + sv.Address, Emoji: &discordgo.ComponentEmoji{Name: "▶️"}}}
}

// button is a gray card button handled by onButton as "srv:<action>:<server id>".
func button(loc, key, emoji, action string, sv Server) discordgo.Button {
	b := discordgo.Button{Label: T(loc, key), Style: discordgo.SecondaryButton, CustomID: "srv:" + action + ":" + strconv.FormatInt(sv.ID, 10)}
	if emoji != "" {
		b.Emoji = &discordgo.ComponentEmoji{Name: emoji}
	}
	return b
}

// playerList queries a server's players and formats them as a scoreboard.
func playerList(loc, addr string, humans int) string {
	players, err := queryPlayers(addr)
	if table := scoreboard(loc, players); err == nil && table != "" {
		return table
	}
	if humans > 0 {
		return T(loc, "players.hidden")
	}
	return T(loc, "players.empty")
}

const nameWidth = 20 // keeps a row on one line on phones

// scoreboard renders players as a code block (where player-chosen names can't render
// as anything), best score first. The name is the last column on purpose: Discord
// draws CJK and emoji with fallback fonts whose width varies by device, so anything
// after a name could drift; numbers before it always line up.
func scoreboard(loc string, players []*a2s.Player) string {
	var rows []*a2s.Player
	for _, p := range players {
		if p.Name != "" { // connecting players have no name yet
			rows = append(rows, p)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		return rows[i].Duration > rows[j].Duration
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s  %s\n", padLeft("#", 2), padLeft(T(loc, "players.col_score"), 5),
		padLeft(T(loc, "players.col_time"), 6), T(loc, "players.col_player"))
	b.WriteString(strings.Repeat("─", 19+nameWidth) + "\n") // "%2d  %5d  %6s  " is 19 cells
	for i, p := range rows {
		name := fitWidth(strings.NewReplacer("`", "'", "\n", " ").Replace(p.Name), nameWidth)
		fmt.Fprintf(&b, "%2d  %5d  %6s  %s\n", i+1, p.Score, playTime(p.Duration), name)
	}
	return "```\n" + truncate(b.String(), 3000) + "```"
}

// playTime formats seconds on the server: "<1m", "27m", "1h 06m".
func playTime(secs float32) string {
	d := time.Duration(secs) * time.Second
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// dispWidth is how many monospace cells s takes: CJK and emoji take 2, combining
// marks (like Thai vowel and tone marks) take 0.
func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Mn, unicode.Me) || r == '​':
		case isWide(r):
			w += 2
		default:
			w++
		}
	}
	return w
}

func isWide(r rune) bool {
	return r >= 0x1100 && r <= 0x115F || // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF || // CJK, kana, Yi
		r >= 0xAC00 && r <= 0xD7A3 || // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF || // CJK compatibility
		r >= 0xFE30 && r <= 0xFE4F || // CJK forms
		r >= 0xFF00 && r <= 0xFF60 || r >= 0xFFE0 && r <= 0xFFE6 || // fullwidth forms
		r >= 0x1F300 && r <= 0x1FAFF || // emoji
		r >= 0x20000 && r <= 0x3FFFD // CJK extensions
}

// fitWidth cuts s to at most w cells, marking a cut with "…".
func fitWidth(s string, w int) string {
	if dispWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := dispWidth(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

func padLeft(s string, w int) string { return strings.Repeat(" ", max(w-dispWidth(s), 0)) + s }

// ---------- sending cards ----------

func (c *ictx) respondV2(ephemeral bool, comps ...discordgo.MessageComponent) {
	flags := discordgo.MessageFlagsIsComponentsV2
	if ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}
	err := c.s.InteractionRespond(c.i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Components: comps, Flags: flags, AllowedMentions: &discordgo.MessageAllowedMentions{}}})
	if err != nil {
		slog.Warn("respond failed", "err", err)
	}
}

// editV2 replaces the interaction's message with components. discordgo's WebhookEdit
// has no flags field, so this sends the request itself.
func (c *ictx) editV2(comps ...discordgo.MessageComponent) {
	body := struct {
		Components      []discordgo.MessageComponent      `json:"components"`
		Flags           discordgo.MessageFlags            `json:"flags"`
		AllowedMentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
	}{comps, discordgo.MessageFlagsIsComponentsV2, &discordgo.MessageAllowedMentions{}}
	url := discordgo.EndpointWebhookMessage(c.i.AppID, c.i.Token, "@original")
	if _, err := c.s.RequestWithBucketID("PATCH", url, body, discordgo.EndpointWebhookToken(c.i.AppID, "")); err != nil {
		slog.Warn("edit failed", "err", err)
	}
}

func textV2(s string) discordgo.TextDisplay { return discordgo.TextDisplay{Content: s} }

// ---------- buttons ----------

// onButton handles the card's Copy IP, Players and Refresh buttons. Custom IDs only
// come from Shiron's own cards, and the server is looked up by ID so a forged ID can
// only ever reach a server some admin already added.
func onButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := &ictx{s: s, i: i, loc: string(i.Locale)}
	// "srv:<action>:<server id>", or "srv:unnotify:<server id>:<map>".
	parts := strings.SplitN(i.MessageComponentData().CustomID, ":", 4)
	if len(parts) < 3 || parts[0] != "srv" {
		return
	}
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	sv, err := db.ServerByID(id)
	if err != nil {
		c.fail("err.server_gone")
		return
	}
	switch parts[1] {
	case "ip":
		c.respondV2(true, textV2(c.t("card.ip", sv.Display())+"\n```\nconnect "+sv.Address+"\n```"))
	case "notify":
		cmdCardNotify(c, *sv)
	case "unnotify":
		if len(parts) == 4 {
			stopAlerts(c, *sv, parts[3])
		}
	case "more": // from a compact card: the full live card, just for this user
		if onCooldown(c.user()) {
			c.fail("err.cooldown", int(queryCooldown.Seconds()))
			return
		}
		c.respondV2(true, skeletonCard(c.loc, *sv))
		st, err := queryInfo(sv.Address)
		if err != nil {
			c.editV2(textV2("❌ " + c.t("err.no_response", sv.Display())))
			return
		}
		c.editV2(fullCard(c.loc, *sv, st, ""))
	// Refresh and Players both check the card is still current first: once the server
	// has moved on, the card becomes history and both buttons disappear from it.
	// ("now" is the Refresh on posts from an earlier build.)
	case "refresh", "now", "players":
		if onCooldown(c.user()) {
			c.fail("err.cooldown", int(queryCooldown.Seconds()))
			return
		}
		// The card is shared, so it stays in the Discord server's language.
		loc := c.loc
		if i.GuildLocale != nil {
			loc = string(*i.GuildLocale)
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
			slog.Warn("defer update failed", "err", err)
			return
		}
		st, err := queryInfo(sv.Address)
		if err != nil {
			_, err = s.FollowupMessageCreate(i.Interaction, false, &discordgo.WebhookParams{
				Content: "❌ " + c.t("err.no_response", sv.Display()), Flags: discordgo.MessageFlagsEphemeral,
				AllowedMentions: &discordgo.MessageAllowedMentions{}})
			if err != nil {
				slog.Warn("followup failed", "err", err)
			}
			return
		}
		// Once the server is on another map, this card is history: keep it and add a
		// note instead of overwriting the map it showed.
		if shown := cardMap(i.Message.Components); shown != "" && !strings.EqualFold(shown, st.Map) {
			note := "-# " + T(loc, "card.since", plain(st.Map), time.Now().Unix())
			if comps, ok := markOutdated(i.Message.Components, note); ok {
				c.editV2(comps...)
			}
			return
		}
		if parts[1] == "players" { // the scoreboard is just for the person who asked
			c.followupV2(textV2("### " + c.t("players.title", st.Humans(), st.Max) + "\n-# " + plain(st.Name) + "\n" +
				playerList(c.loc, sv.Address, st.Humans()) + "\n-# " + c.t("players.footer", time.Now().Unix())))
			return
		}
		c.editV2(serverCard(loc, *sv, st, cardPrevMap(i.Message.Components)))
	}
}

// followupV2 sends a private follow-up after a deferred button response.
func (c *ictx) followupV2(comps ...discordgo.MessageComponent) {
	_, err := c.s.FollowupMessageCreate(c.i.Interaction, false, &discordgo.WebhookParams{
		Components: comps, Flags: discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{}})
	if err != nil {
		slog.Warn("followup failed", "err", err)
	}
}

// mapAlerts splits a user's alerts that apply to this map on this server into exact
// ones (named after the map, for this server or all servers) and covering ones
// (wildcards like ze_*). Stop only removes exact ones, so it never deletes a wildcard
// that also covers other maps.
func mapAlerts(user string, sv Server, key string) (exact, covering []Notify) {
	list, _ := db.Notifies(user, sv.GuildID)
	for _, n := range list {
		if !n.AllServers() && n.Server.ID != sv.ID {
			continue
		}
		switch {
		case n.Pattern == key:
			exact = append(exact, n)
		case matchMap(n.Pattern, key):
			covering = append(covering, n)
		}
	}
	return exact, covering
}

// stopButton turns off alerts for one map ("srv:unnotify:<server id>:<map>").
func stopButton(loc string, sv Server, key string) discordgo.Button {
	b := button(loc, "notify.stop", "🔕", "unnotify", sv)
	b.CustomID += ":" + key // map names are at most 64 characters, so this fits Discord's 100
	return b
}

// cmdCardNotify is 🔔 Notify me on a card. The card is shared, so the answer is
// private: either "I'll alert you" or "you already get alerts", each with a Stop
// alerts button. It works on old cards too, as a way to say "tell me when this is back".
func cmdCardNotify(c *ictx, sv Server) {
	key := strings.ToLower(cardMap(c.i.Message.Components))
	if !validMapName(key) {
		c.fail("err.bad_pattern")
		return
	}
	stop := discordgo.ActionsRow{Components: []discordgo.MessageComponent{stopButton(c.loc, sv, key)}}
	if exact, _ := mapAlerts(c.user(), sv, key); len(exact) > 0 {
		c.respondV2(true, textV2(c.t("notify.already", code(key), notifyWhere(c.loc, exact[0]))), stop)
		return
	}
	existing, err := db.Notifies(c.user(), sv.GuildID)
	if err != nil {
		c.fail("err.internal")
		return
	}
	if len(existing) >= maxNotifiesPerUser {
		c.fail("err.too_many_notifies", maxNotifiesPerUser)
		return
	}
	if err := db.AddNotify(c.user(), sv.GuildID, sv.ID, key); err != nil && !isUnique(err) {
		c.fail("err.internal")
		return
	}
	c.respondV2(true, textV2("✅ "+c.t("notify.added", code(key), sv.Display(), c.t("mode."+db.Delivery(c.user())))), stop)
}

// stopAlerts is 🔕 Stop alerts: removes the user's alerts named after this map (on this
// server or all servers). A wildcard that covers it is left alone, with a pointer to
// /notify list. On a private message the message itself changes; on a DM card a
// private note is sent so the card stays as it was.
func stopAlerts(c *ictx, sv Server, key string) {
	exact, covering := mapAlerts(c.user(), sv, key)
	for _, n := range exact {
		if _, err := db.RemoveNotify(c.user(), n.ID); err != nil {
			c.fail("err.internal")
			return
		}
	}
	var msg string
	switch {
	case len(exact) > 0 && len(covering) > 0:
		msg = "🔕 " + c.t("notify.stopped", code(key)) + "\n" + c.t("notify.covered", code(key), code(covering[0].Pattern))
	case len(exact) > 0:
		msg = "🔕 " + c.t("notify.stopped", code(key))
	case len(covering) > 0:
		msg = c.t("notify.covered", code(key), code(covering[0].Pattern))
	default:
		msg = c.t("notify.nothing_to_stop", code(key))
	}
	if m := c.i.Message; m != nil && m.Flags&discordgo.MessageFlagsEphemeral != 0 {
		c.update([]discordgo.MessageComponent{textV2(msg)})
		return
	}
	c.respondV2(true, textV2(msg))
}

// withStopButton personalizes a card for one user (a DM alert): Notify me becomes
// 🔕 Stop alerts, since we know they're subscribed.
func withStopButton(loc string, card discordgo.Container, sv Server, key string) discordgo.Container {
	comps := append([]discordgo.MessageComponent(nil), card.Components...)
	row, ok := comps[len(comps)-1].(discordgo.ActionsRow)
	if !ok {
		return card
	}
	btns := append([]discordgo.MessageComponent(nil), row.Components...)
	for i, b := range btns {
		if btn, ok := b.(discordgo.Button); ok && strings.HasPrefix(btn.CustomID, "srv:notify:") {
			btns[i] = stopButton(loc, sv, key)
		}
	}
	comps[len(comps)-1] = discordgo.ActionsRow{Components: btns}
	card.Components = comps
	return card
}
