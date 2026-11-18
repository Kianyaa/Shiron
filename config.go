package main

import (
	"errors"
	"os"
	"time"
)

type Config struct {
	Token string
	DB    string
	Poll  time.Duration
}

func loadConfig() (Config, error) {
	c := Config{Token: os.Getenv("SHIRON_TOKEN"), DB: os.Getenv("SHIRON_DB"), Poll: 60 * time.Second}
	if c.Token == "" {
		return c, errors.New("SHIRON_TOKEN is not set")
	}
	if c.DB == "" {
		c.DB = "shiron.db"
	}
	if p := os.Getenv("SHIRON_POLL"); p != "" {
		d, err := time.ParseDuration(p)
		if err != nil || d < 10*time.Second {
			return c, errors.New("SHIRON_POLL must be a duration of at least 10s, e.g. 60s")
		}
		c.Poll = d
	}
	return c, nil
}
