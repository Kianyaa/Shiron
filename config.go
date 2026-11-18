package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const defaultMapImages = "https://raw.githubusercontent.com/ghostcap-gaming/cs2-map-images/main/cs2/%s.png"

type Config struct {
	Token      string
	DB         string
	Poll       time.Duration
	ConnectURL string // https page that forwards to steam://connect; "" = no Join button
	MapImages  string // URL template with one %s for the map name; "" = no pictures
}

// cfg is set once at startup; zero value (no pictures, no Join button) is used in tests.
var cfg Config

func loadConfig() (Config, error) {
	c := Config{
		Token:      os.Getenv("SHIRON_TOKEN"),
		DB:         envOr("SHIRON_DB", "shiron.db"),
		Poll:       60 * time.Second,
		ConnectURL: os.Getenv("SHIRON_CONNECT_URL"),
		MapImages:  envOr("SHIRON_MAP_IMAGES", defaultMapImages),
	}
	if c.Token == "" {
		// Reading the token from a file keeps it out of shell history and service files.
		file := envOr("SHIRON_TOKEN_FILE", "token.txt")
		b, err := os.ReadFile(file)
		if err != nil {
			return c, fmt.Errorf("no bot token: set SHIRON_TOKEN_FILE or put the token in %s", file)
		}
		c.Token = strings.TrimSpace(string(b))
	}
	if c.Token == "" {
		return c, errors.New("the bot token is empty")
	}
	if p := os.Getenv("SHIRON_POLL"); p != "" {
		d, err := time.ParseDuration(p)
		if err != nil || d < 10*time.Second {
			return c, errors.New("SHIRON_POLL must be a duration of at least 10s, e.g. 60s")
		}
		c.Poll = d
	}
	if c.ConnectURL != "" && !strings.HasPrefix(c.ConnectURL, "https://") {
		return c, errors.New("SHIRON_CONNECT_URL must start with https://")
	}
	if c.MapImages == "none" {
		c.MapImages = ""
	} else if c.MapImages != "" && (strings.Count(c.MapImages, "%s") != 1 || !strings.HasPrefix(c.MapImages, "https://")) {
		return c, errors.New("SHIRON_MAP_IMAGES must be an https:// URL containing exactly one %s, or \"none\"")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
