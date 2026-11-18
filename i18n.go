package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed locales/*.json
var localeFS embed.FS

var locales = map[string]map[string]string{}

func init() {
	for _, l := range []string{"en", "th", "zh-CN"} {
		b, err := localeFS.ReadFile("locales/" + l + ".json")
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("locales/%s.json: %v", l, err))
		}
		locales[l] = m
	}
}

// lang maps a Discord locale ("th", "zh-CN", "en-US", ...) to one of our locale files.
func lang(loc string) string {
	switch {
	case strings.HasPrefix(loc, "th"):
		return "th"
	case loc == "zh-CN":
		return "zh-CN"
	}
	return "en"
}

// T returns the message for key in the given Discord locale, falling back to English.
func T(loc, key string, args ...any) string {
	msg := locales[lang(loc)][key]
	if msg == "" {
		msg = locales["en"][key]
	}
	if msg == "" {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}
