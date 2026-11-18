package main

import (
	"context"
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

func pollOnce(s *discordgo.Session) {
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

	for addr, rows := range byAddr {
		st := results[addr]
		if st == nil {
			continue // no answer this round: keep last_map, post nothing
		}
		for _, sv := range rows {
			if changed(sv.LastMap, st.Map) {
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

func guildLocale(s *discordgo.Session, guildID string) string {
	if g, err := s.State.Guild(guildID); err == nil {
		return g.PreferredLocale
	}
	return "en-US"
}

// announce posts a map change to the tracking channel (if enough humans are on)
// and alerts everyone whose /notify pattern matches (always).
func announce(s *discordgo.Session, sv Server, st *Status) {
	loc := guildLocale(s, sv.GuildID)
	embed := statusEmbed(loc, sv.Address, st)
	embed.Title, embed.Description = T(loc, "post.title", st.Map), st.Name

	if st.Humans() >= sv.MinPlayers {
		if _, err := s.ChannelMessageSendEmbed(sv.ChannelID, embed); err != nil {
			slog.Warn("post map change", "guild", sv.GuildID, "channel", sv.ChannelID, "err", err)
		}
	}

	notifies, err := db.NotifiesFor(sv.ID)
	if err != nil {
		slog.Error("load notifications", "err", err)
		return
	}
	seen := map[string]bool{} // a user with "de_*" and "de_dust2" gets one alert
	var mentions []string
	for _, n := range notifies {
		if seen[n.UserID] || !matchMap(n.Pattern, st.Map) {
			continue
		}
		seen[n.UserID] = true
		if db.Delivery(n.UserID) == "channel" {
			mentions = append(mentions, n.UserID)
			continue
		}
		ch, err := s.UserChannelCreate(n.UserID)
		if err == nil {
			_, err = s.ChannelMessageSendEmbed(ch.ID, embed)
		}
		if err != nil {
			slog.Info("DM failed (user may have DMs off)", "user", n.UserID, "err", err)
		}
	}
	// 50 mentions per message keeps us under Discord's 2000-character limit.
	for i := 0; i < len(mentions); i += 50 {
		batch := mentions[i:min(i+50, len(mentions))]
		var b strings.Builder
		for _, id := range batch {
			b.WriteString("<@" + id + "> ")
		}
		b.WriteString(T(loc, "notify.channel", st.Map, st.Name))
		_, err := s.ChannelMessageSendComplex(sv.ChannelID, &discordgo.MessageSend{Content: b.String(),
			AllowedMentions: &discordgo.MessageAllowedMentions{Users: batch}})
		if err != nil {
			slog.Warn("post notify mentions", "channel", sv.ChannelID, "err", err)
		}
	}
}
