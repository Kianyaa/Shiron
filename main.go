// Shiron is a Discord bot that tracks CS2 servers: map-change posts, live map and
// player lookups, and per-user map notifications.
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/bwmarrin/discordgo"
)

var db *Store

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "query" {
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: shiron query <ip[:port]>")
			os.Exit(2)
		}
		if err := runQueryCLI(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	slog.SetDefault(slog.New(&logHandler{w: os.Stdout}))
	if err := run(); err != nil {
		say("\n")
		lines := []string{paint(cRed+";1", "✖ Shiron stopped"), ""}
		lines = append(lines, wrap(friendly(err), 64)...)
		panel(cRed, lines...)
		if runtime.GOOS == "windows" && isTTY {
			// A double-clicked window would close before the error could be read.
			say("\n  %s", paint(cDim, "Press Enter to close this window."))
			bufio.NewReader(os.Stdin).ReadString('\n')
		}
		os.Exit(1)
	}
}

// wrap breaks text into lines of at most width characters, at spaces.
func wrap(text string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(text) {
		if line != "" && len([]rune(line))+1+len([]rune(w)) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(lines, line)
}

// bioText turns "/map" and "/servers add" in the bio into clickable command mentions
// (</map:ID>), using the IDs Discord gave the commands. Discord can't mention a command
// that has subcommands by itself, so those need the subcommand written out.
func bioText(bio string, cmds []*discordgo.ApplicationCommand) string {
	var names []string // "map", "servers add", ...
	ids := map[string]string{}
	for _, c := range cmds {
		if len(c.Options) > 0 && c.Options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
			for _, sub := range c.Options {
				names, ids[c.Name+" "+sub.Name] = append(names, c.Name+" "+sub.Name), c.ID
			}
			continue
		}
		names, ids[c.Name] = append(names, c.Name), c.ID
	}
	// Longest first, so "/servers add" is matched before anything shorter.
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	var r []string
	for _, n := range names {
		r = append(r, "/"+n, "</"+n+":"+ids[n]+">")
	}
	return strings.NewReplacer(r...).Replace(bio)
}

// friendly explains the errors people actually hit when starting the bot.
func friendly(err error) string {
	if strings.Contains(err.Error(), "4004") {
		return "your bot token is invalid or was reset. In the Discord Developer Portal, open Bot, click Reset Token, and paste the new token into token.txt."
	}
	return err.Error()
}

func run() error {
	banner(version())
	setTitle("Shiron · starting…")
	var err error
	if cfg, err = loadConfig(); err != nil {
		return err
	}
	stepOK("Settings loaded")
	if db, err = openDB(cfg.DB); err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	stepOK(fmt.Sprintf("Database ready %s", paint(cDim, fmt.Sprintf("· %s · %d tracked · %s",
		cfg.DB, trackedCount(), plural(db.CountAlerts(), "alert")))))

	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return err
	}
	s.Identify.Intents = discordgo.IntentsGuilds // no privileged intents needed
	s.AddHandler(onInteraction)
	watchGuilds(s)

	done := spin("Connecting to Discord…")
	if err := s.Open(); err != nil {
		done(false, "Couldn't connect to Discord")
		return fmt.Errorf("connect to Discord: %w", err)
	}
	defer s.Close()
	done(true, "Connected as "+paint(cBold, s.State.User.Username))

	registered, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", commandDefs())
	if err != nil {
		return fmt.Errorf("register commands: %w", err)
	}
	stepOK(fmt.Sprintf("%d slash commands registered", len(registered)))
	if cfg.Bio != "" {
		// Not fatal: the bot works fine with whatever bio the Developer Portal has.
		body := map[string]string{"description": bioText(cfg.Bio, registered)}
		if n := len([]rune(body["description"])); n > 400 {
			stepWarn(fmt.Sprintf("Bio not set: %d characters with command links, Discord allows 400. Shorten SHIRON_BIO.", n))
		} else if _, err := s.RequestWithBucketID("PATCH", discordgo.EndpointAPI+"applications/@me", body, discordgo.EndpointAPI+"applications/@me"); err != nil {
			stepWarn("Bio not updated: " + err.Error())
		} else {
			stepOK("Profile bio updated")
		}
	}

	join := paint(cGreen, "on")
	if cfg.ConnectURL == "" {
		join = paint(cYellow, "off") + paint(cDim, " (set SHIRON_CONNECT_URL in shiron.conf)")
	}
	s.State.RLock()
	guilds := len(s.State.Guilds)
	s.State.RUnlock()
	say("\n")
	label := func(s string) string { return paint(cDim, fmt.Sprintf("%-16s", s)) }
	panel(cBlurple,
		paint(cGreen, "●")+" "+paint(cBold, "Online as "+s.State.User.Username),
		"",
		label("Discord servers")+fmt.Sprint(guilds),
		label("CS2 servers")+fmt.Sprintf("%d tracked", trackedCount()),
		label("Alerts")+fmt.Sprint(db.CountAlerts()),
		label("Checks")+"every "+cfg.Poll.String(),
		label("Join button")+join,
		label("Invite")+paint(cCyan, inviteURL(s.State.User.ID)),
	)
	say("\n  %s\n\n", paint(cDim, "Keep this window open. Close it or press Ctrl+C to stop the bot. Live status below:"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runPoller(ctx, s, cfg.Poll)
	go runDMSender(ctx, s)
	<-ctx.Done()
	say("\n  %s\n", paint(cYellow, "Shutting down…"))
	setTitle("Shiron · stopped")
	return nil
}

// watchGuilds prints when Shiron is added to or removed from a Discord server, and
// cleans up a server's data when Shiron is removed. Servers Shiron was already in
// arrive at startup too; only ones not in the first list count as "added".
func watchGuilds(s *discordgo.Session) {
	var mu sync.Mutex
	known := map[string]bool{}
	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		mu.Lock()
		defer mu.Unlock()
		for _, g := range r.Guilds {
			known[g.ID] = true
		}
	})
	s.AddHandler(func(s *discordgo.Session, g *discordgo.GuildCreate) {
		mu.Lock()
		isNew := !known[g.ID]
		known[g.ID] = true
		mu.Unlock()
		if isNew {
			s.State.RLock()
			n := len(s.State.Guilds)
			s.State.RUnlock()
			guildEvent(true, g.Name, n)
		}
	})
	s.AddHandler(func(s *discordgo.Session, g *discordgo.GuildDelete) {
		if g.Unavailable {
			return // Discord outage, not a kick
		}
		mu.Lock()
		delete(known, g.ID)
		mu.Unlock()
		name := g.ID
		if g.BeforeDelete != nil && g.BeforeDelete.Name != "" {
			name = g.BeforeDelete.Name
		}
		s.State.RLock()
		n := len(s.State.Guilds)
		s.State.RUnlock()
		guildEvent(false, name, n)
		if err := db.DeleteGuild(g.ID); err != nil {
			slog.Error("cleanup guild", "guild", g.ID, "err", err)
		}
	})
}
