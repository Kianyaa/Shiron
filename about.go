package main

// about.go: /about — who Shiron is, its version and what's new (from CHANGELOG.md),
// a few live stats, and buttons to invite it or read the full changelog.

import (
	_ "embed"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

//go:embed CHANGELOG.md
var changelogFile string

// startedAt is when this run of Shiron started, for "online since".
var startedAt = time.Now()

type release struct {
	Version, Date string
	Notes         string // the bullet lines, as written
}

// releases parses CHANGELOG.md: each "## v1.0 · 2026-11-19" heading starts a release,
// newest first. Text before the first heading is ignored.
func releases(changelog string) []release {
	var out []release
	for _, block := range strings.Split(changelog, "\n## ")[1:] {
		head, body, _ := strings.Cut(block, "\n")
		version, date, _ := strings.Cut(head, "·")
		out = append(out, release{strings.TrimSpace(version), strings.TrimSpace(date), strings.TrimSpace(body)})
	}
	return out
}

// version is the newest release, shown in /about and the startup window.
func version() release {
	if r := releases(changelogFile); len(r) > 0 {
		return r[0]
	}
	return release{Version: "dev"}
}

// trackedCount is how many distinct CS2 servers Shiron watches across all guilds.
func trackedCount() int {
	list, _ := db.Servers("")
	seen := map[string]bool{}
	for _, sv := range list {
		seen[sv.Address] = true
	}
	return len(seen)
}

func aboutView(loc string, s *discordgo.Session) []discordgo.MessageComponent {
	v := version()
	s.State.RLock()
	guilds := len(s.State.Guilds)
	s.State.RUnlock()
	head := discordgo.TextDisplay{Content: "## " + s.State.User.Username + "\n" + T(loc, "about.description") +
		"\n-# " + T(loc, "about.version", v.Version, v.Date)}
	var top discordgo.MessageComponent = head
	if avatar := s.State.User.AvatarURL("256"); avatar != "" {
		top = discordgo.Section{Components: []discordgo.MessageComponent{head},
			Accessory: discordgo.Thumbnail{Media: discordgo.UnfurledMediaItem{URL: avatar}}}
	}
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(colorShiron), Components: []discordgo.MessageComponent{
		top,
		discordgo.Separator{},
		textV2("**" + T(loc, "about.whats_new", v.Version) + "**\n" + truncate(v.Notes, 1500)),
		textV2(T(loc, "about.stats", guilds, trackedCount(), startedAt.Unix())),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: T(loc, "about.invite"), Style: discordgo.LinkButton, URL: inviteURL(s.State.User.ID), Emoji: &discordgo.ComponentEmoji{Name: "➕"}},
			discordgo.Button{Label: T(loc, "about.changelog"), Style: discordgo.SecondaryButton, CustomID: "about:log", Emoji: &discordgo.ComponentEmoji{Name: "📜"}},
		}},
	}}}
}

// changelogView is the last few releases, shown privately by Full changelog.
func changelogView(loc string) []discordgo.MessageComponent {
	rs := releases(changelogFile)
	var b strings.Builder
	b.WriteString("### " + T(loc, "about.changelog") + "\n")
	for i, r := range rs {
		if i == 5 {
			break
		}
		fmt.Fprintf(&b, "**%s** · %s\n%s\n\n", r.Version, r.Date, r.Notes)
	}
	return []discordgo.MessageComponent{textV2(truncate(strings.TrimSpace(b.String()), 3900))}
}

func cmdAbout(c *ictx) { c.respondV2(false, aboutView(c.loc, c.s)...) }

// onAbout handles the Full changelog button.
func onAbout(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := &ictx{s: s, i: i, loc: string(i.Locale)}
	if i.MessageComponentData().CustomID != "about:log" {
		slog.Warn("unknown about button", "id", i.MessageComponentData().CustomID)
		return
	}
	c.respondV2(true, changelogView(c.loc)...)
}
