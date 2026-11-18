package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	imageCache sync.Map // lowercase map name -> picture URL, or "" when there is none
	httpClient = &http.Client{Timeout: 5 * time.Second}
)

// mapImage returns a picture URL for a map, or "" if there isn't one. It tries the
// picture collection (SHIRON_MAP_IMAGES), then the map's Steam Workshop preview.
// Only plain map names are looked up, so a server can't make us fetch arbitrary paths.
func mapImage(mapName string) string {
	if cfg.MapImages == "" {
		return ""
	}
	name := path.Base(mapName) // workshop maps can look like "workshop/123/ze_x"
	if !validMapName(name) {
		return ""
	}
	key := strings.ToLower(name)
	if v, ok := imageCache.Load(key); ok {
		return v.(string)
	}
	img, err := findMapImage(name)
	if err != nil {
		return "" // network hiccup: not cached, so we try again next time
	}
	// shortcut: results are cached until restart; restart to pick up newly published pictures.
	imageCache.Store(key, img)
	return img
}

func validMapName(name string) bool {
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return name != ""
}

func findMapImage(name string) (string, error) {
	// The collection has files like "ze_ATIX_apocalypse_p.png", so try the exact name first.
	tries := []string{name}
	if lower := strings.ToLower(name); lower != name {
		tries = append(tries, lower)
	}
	for _, n := range tries {
		u := fmt.Sprintf(cfg.MapImages, n)
		resp, err := httpClient.Head(u)
		if err != nil {
			return "", err
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return u, nil
		}
	}
	return workshopPreview(name)
}

var workshopIDRe = regexp.MustCompile(`sharedfiles/filedetails/\?id=(\d+)`)

// workshopIDs pulls up to 10 unique item IDs out of a Workshop search page.
func workshopIDs(html string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, m := range workshopIDRe.FindAllStringSubmatch(html, -1) {
		if !seen[m[1]] && len(ids) < 10 {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	return ids
}

type workshopItem struct {
	Title      string `json:"title"`
	AppID      int    `json:"consumer_app_id"`
	PreviewURL string `json:"preview_url"`
}

// pickPreview returns the preview of the CS2 item whose title is exactly the map name.
func pickPreview(items []workshopItem, name string) string {
	for _, it := range items {
		if it.AppID == 730 && strings.EqualFold(strings.TrimSpace(it.Title), name) && strings.HasPrefix(it.PreviewURL, "https://") {
			return it.PreviewURL
		}
	}
	return ""
}

// workshopPreview finds a CS2 Workshop map by name and returns its preview picture.
// shortcut: scrapes the public Workshop search page because Steam's search API needs a
// key; if Steam changes that page, switch to IPublishedFileService/QueryFiles with a key.
func workshopPreview(name string) (string, error) {
	resp, err := httpClient.Get("https://steamcommunity.com/workshop/browse/?appid=730&browsesort=textsearch&section=readytouseitems&searchtext=" + url.QueryEscape(name))
	if err != nil {
		return "", err
	}
	page, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("workshop search: %s", resp.Status)
	}
	ids := workshopIDs(string(page))
	if len(ids) == 0 {
		return "", nil
	}

	form := url.Values{"itemcount": {strconv.Itoa(len(ids))}}
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	resp, err = httpClient.PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Response struct {
			Items []workshopItem `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return "", err
	}
	return pickPreview(out.Response.Items, name), nil
}
