// Shiron is a Discord bot that tracks CS2 servers: map-change posts, live map and
// player lookups, and per-user map notifications.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
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
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var err error
	if cfg, err = loadConfig(); err != nil {
		return err
	}
	if db, err = openDB(cfg.DB); err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return err
	}
	s.Identify.Intents = discordgo.IntentsGuilds // no privileged intents needed
	s.AddHandler(onInteraction)
	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		slog.Info("connected", "bot", r.User.String(), "guilds", len(r.Guilds))
	})
	s.AddHandler(func(_ *discordgo.Session, g *discordgo.GuildDelete) {
		if g.Unavailable {
			return // Discord outage, not a kick
		}
		if err := db.DeleteGuild(g.ID); err != nil {
			slog.Error("cleanup guild", "guild", g.ID, "err", err)
		}
	})
	if err := s.Open(); err != nil {
		return fmt.Errorf("connect to Discord: %w", err)
	}
	defer s.Close()

	if _, err := s.ApplicationCommandBulkOverwrite(s.State.User.ID, "", commandDefs()); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}
	slog.Info("ready", "invite", inviteURL(s.State.User.ID), "poll", cfg.Poll)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runPoller(ctx, s, cfg.Poll)
	<-ctx.Done()
	slog.Info("shutting down")
	return nil
}
