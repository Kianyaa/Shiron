package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const queryWorkers = 32

// changed reports a real map change. A blank previous map (new server or first
// sighting) is not a change, so restarts and new servers don't trigger posts.
func changed(prev, cur string) bool {
	return prev != "" && cur != "" && !strings.EqualFold(prev, cur)
}

func runPoller(ctx context.Context, s *discordgo.Session, every time.Duration) {
	t := time.NewTicker(every) // a slow poll just skips ticks; it never overlaps itself
	defer t.Stop()
	for {
		pollOnce(s)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// guard logs a panic instead of letting it crash the bot. Use as `defer guard("what")`.
func guard(what string) {
	if r := recover(); r != nil {
		slog.Error("recovered from panic", "in", what, "err", r)
	}
}

func pollOnce(s *discordgo.Session) {
	defer guard("poll")
	servers, err := db.Servers("")
	if err != nil {
		slog.Error("poll: list servers", "err", err)
		return
	}
	// Query each address once, even if several Discord servers track it.
	byAddr := map[string][]Server{}
	for _, sv := range servers {
		byAddr[sv.Address] = append(byAddr[sv.Address], sv)
	}
	results := make(map[string]*Status, len(byAddr))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, queryWorkers)
	for addr := range byAddr {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer guard("query " + addr)
			sem <- struct{}{}
			defer func() { <-sem }()
			if st, err := queryInfo(addr); err == nil {
				mu.Lock()
				results[addr] = st
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	updatePresence(s, len(byAddr))
	s.State.RLock()
	guilds := len(s.State.Guilds)
	s.State.RUnlock()
	reportStatus(liveStatus{guilds: guilds, tracked: len(byAddr), up: len(results), alerts: db.CountAlerts()})
	serverHealth.forget(byAddr)
	for addr, rows := range byAddr {
		st := results[addr]
		if change, since := serverHealth.update(addr, st != nil, time.Now()); change != "" {
			// &rows[i]: a pause from this post is seen by the map-change post below too.
			for i := range rows {
				postStatus(s, &rows[i], change, since)
			}
		}
		if st == nil {
			continue // no answer this round: keep last_map, post nothing
		}
		// The first answer since Shiron started only records the map: the saved one may
		// be from before a long downtime, so "changed from" it would be wrong.
		baseline := serverHealth.firstAnswer(addr)
		for i := range rows {
			sv := &rows[i]
			if !baseline && changed(sv.LastMap, st.Map) {
				announce(s, sv, st)
			}
			if sv.LastMap != st.Map || sv.Name != st.Name {
				if err := db.UpdateState(sv.ID, st.Map, st.Name); err != nil {
					slog.Error("poll: update state", "err", err)
				}
			}
		}
	}
}

var lastPresence string // only the poller goroutine touches this

// updatePresence shows "Tracking 2 servers on 2 Discord servers" on the bot's profile.
// Only sent when it changes.
func updatePresence(s *discordgo.Session, tracked int) {
	s.State.RLock()
	guilds := len(s.State.Guilds)
	s.State.RUnlock()
	status := presenceText(tracked, guilds)
	if status == lastPresence {
		return
	}
	// A custom status shows the text as-is, without a "Watching"/"Playing" prefix.
	if err := s.UpdateCustomStatus(status); err != nil {
		slog.Warn("update status", "err", err)
		return
	}
	lastPresence = status
}

func presenceText(tracked, guilds int) string {
	return fmt.Sprintf("Tracking %s on %s", plural(tracked, "server"), plural(guilds, "Discord server"))
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// offlineAfter missed checks in a row mark a server offline, so one lost reply
// (UDP drops happen) doesn't spam the channel.
const offlineAfter = 3

// health tracks missed checks per address. Only the poller goroutine uses it.
// shortcut: kept in memory, so a restart forgets who was offline (a server still down
// gets one more "offline" post, and one that recovers meanwhile gets no "back online");
// persist it in the servers table if that becomes annoying.
type health struct {
	misses    map[string]int
	down      map[string]bool
	answered  map[string]bool      // has answered since Shiron started
	lastOK    map[string]time.Time // last answer
	downSince map[string]time.Time // last answer before it went offline
}

func newHealth() health {
	return health{misses: map[string]int{}, down: map[string]bool{}, answered: map[string]bool{},
		lastOK: map[string]time.Time{}, downSince: map[string]time.Time{}}
}

var serverHealth = newHealth()

// firstAnswer reports whether this is the server's first answer since Shiron started.
func (h health) firstAnswer(addr string) bool {
	if h.answered[addr] {
		return false
	}
	h.answered[addr] = true
	return true
}

// update records one check made at now and returns "offline" or "online" when the
// server's state flips, otherwise "". With it comes the server's last answer before it
// went down (zero if it hasn't answered since Shiron started), for "last seen" and
// "offline for".
func (h health) update(addr string, answered bool, now time.Time) (string, time.Time) {
	if answered {
		h.misses[addr] = 0
		h.lastOK[addr] = now
		if h.down[addr] {
			since := h.downSince[addr]
			delete(h.down, addr)
			delete(h.downSince, addr)
			return "online", since
		}
		return "", time.Time{}
	}
	h.misses[addr]++
	if h.misses[addr] == offlineAfter && !h.down[addr] {
		h.down[addr] = true
		h.downSince[addr] = h.lastOK[addr]
		return "offline", h.lastOK[addr]
	}
	return "", time.Time{}
}

// downtime formats how long a server was offline, at any length: "<1m", "25m",
// "1h 06m", "2d 3h", "1y 35d". Whole minutes only (no float rounding), and anything
// under a minute, including a clock that jumped backwards, is "<1m".
func downtime(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	mins := int64(d / time.Minute)
	days := mins / (24 * 60)
	switch {
	case days >= 365:
		return fmt.Sprintf("%dy %dd", days/365, days%365)
	case days >= 1:
		return fmt.Sprintf("%dd %dh", days, mins/60%24)
	case mins >= 60:
		return fmt.Sprintf("%dh %02dm", mins/60, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

// forget drops addresses nobody tracks anymore.
func (h health) forget(tracked map[string][]Server) {
	for addr := range h.misses {
		if _, ok := tracked[addr]; !ok {
			delete(h.misses, addr)
			delete(h.down, addr)
			delete(h.answered, addr)
			delete(h.lastOK, addr)
			delete(h.downSince, addr)
		}
	}
}

// statusText is the offline / back-online line. since is the last answer before the
// server went down; when it's unknown (down since Shiron started) the time is left out.
func statusText(loc, change, name string, since, now time.Time) string {
	switch {
	case since.IsZero():
		return T(loc, "status."+change, plain(name))
	case change == "offline":
		return T(loc, "status.offline_seen", plain(name), since.Unix()) // Discord counts this up live
	}
	return T(loc, "status.online_after", plain(name), downtime(now.Sub(since)))
}

// postStatus tells a tracking channel that its server went offline or came back.
func postStatus(s *discordgo.Session, sv *Server, change string, since time.Time) {
	loc := guildLocale(s, sv.GuildID)
	name := sv.Name
	if name == "" {
		name = sv.Address
	}
	postToChannel(s, sv, nil, textV2(statusText(loc, change, name, since, time.Now())))
}

func guildLocale(s *discordgo.Session, guildID string) string {
	if g, err := s.State.Guild(guildID); err == nil {
		return g.PreferredLocale
	}
	return "en-US"
}

// send posts components-v2 parts to a channel, pinging only the given users.
func send(s *discordgo.Session, channelID string, users []string, parts ...discordgo.MessageComponent) error {
	_, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Components: parts,
		Flags: discordgo.MessageFlagsIsComponentsV2, AllowedMentions: &discordgo.MessageAllowedMentions{Users: users}})
	return err
}

// mentionText is the line above the card that pings users whose alert matched.
func mentionText(loc string, users []string, st *Status) discordgo.TextDisplay {
	var b strings.Builder
	for _, id := range users {
		b.WriteString("<@" + id + "> ")
	}
	b.WriteString(T(loc, "notify.channel", code(st.Map), plain(st.Name)))
	return textV2(b.String())
}

// announce posts a map change to the tracking channel (if enough humans are on)
// and alerts everyone whose /notify pattern matches (always). Channel mentions ride
// on the same message as the card, so the channel gets one message, not two.
func announce(s *discordgo.Session, sv *Server, st *Status) {
	loc := guildLocale(s, sv.GuildID)
	card := serverCard(loc, *sv, st, sv.LastMap)

	notifies, err := db.NotifiesFor(*sv)
	if err != nil {
		slog.Error("load notifications", "err", err)
	}
	seen := map[string]bool{} // a user with "de_*" and "de_dust2" gets one alert
	var mentions []string
	for _, n := range notifies {
		if seen[n.UserID] || !matchMap(n.Pattern, st.Map) {
			continue
		}
		seen[n.UserID] = true
		if n.Delivery == "channel" {
			mentions = append(mentions, n.UserID)
			continue
		}
		queueDM(n.UserID, withStopButton(loc, card, *sv, strings.ToLower(st.Map))) // a DM is theirs: offer Stop alerts
	}

	// 50 mentions per message keeps the text well under Discord's limits.
	var batches [][]string
	for i := 0; i < len(mentions); i += 50 {
		batches = append(batches, mentions[i:min(i+50, len(mentions))])
	}
	if st.Humans() < sv.MinPlayers && len(batches) == 0 {
		return // too few players to post, and nobody asked to be pinged
	}
	parts, users := []discordgo.MessageComponent{card}, []string(nil)
	if len(batches) > 0 {
		parts, users = []discordgo.MessageComponent{mentionText(loc, batches[0], st), card}, batches[0]
	}
	postToChannel(s, sv, users, parts...)
	for _, batch := range batches[min(1, len(batches)):] {
		postToChannel(s, sv, batch, mentionText(loc, batch, st))
	}
}
