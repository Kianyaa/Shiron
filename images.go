package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // Workshop previews
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	imageCache sync.Map // lowercase map name -> imageEntry
	httpClient = &http.Client{Timeout: 5 * time.Second}
)

// imageEntry is a looked-up picture; url "" means none was found at time at.
type imageEntry struct {
	url string
	at  time.Time
}

// missRetry is how long "no picture" is remembered before looking again, so maps
// uploaded or fixed later get their picture without a restart.
const missRetry = 6 * time.Hour

const (
	lookupAttempts   = 3
	lookupRetryDelay = 2 * time.Second
)

// mapPicture is the picture for a map in one Discord server, from the most to the
// least specific source: the admins' override (/servers picture), the Workshop
// collections they linked (/servers collection), then the automatic lookup.
func mapPicture(guildID, mapName string) string {
	if db != nil {
		if u := db.MapPicture(guildID, mapKey(mapName)); u != "" {
			return u
		}
		for _, id := range db.Collections(guildID) {
			if u := collectionPicture(id, mapName); u != "" {
				return u
			}
		}
	}
	return mapImage(mapName)
}

// cachedMapPicture is mapPicture without any network, for loading cards that must be
// sent instantly: "" unless the picture is already known.
func cachedMapPicture(guildID, mapName string) string {
	keys := []string{mapKey(mapName)}
	if db != nil {
		if u := db.MapPicture(guildID, mapKey(mapName)); u != "" {
			return u
		}
		var fromCollections []string
		for _, id := range db.Collections(guildID) {
			fromCollections = append(fromCollections, "c:"+id+":"+mapKey(mapName))
		}
		keys = append(fromCollections, keys...)
	}
	for _, k := range keys {
		if v, ok := imageCache.Load(k); ok && v.(imageEntry).url != "" {
			return v.(imageEntry).url
		}
	}
	return ""
}

// mapKey is how map names are stored: workshop maps can look like "workshop/123/ze_x".
func mapKey(mapName string) string { return strings.ToLower(path.Base(mapName)) }

// mapImage returns an automatic picture URL for a map, or "" if there isn't one. It
// tries the picture collection (SHIRON_MAP_IMAGES), then the map's Steam Workshop
// preview. Only plain map names are looked up, so a server can't make us fetch
// arbitrary paths.
func mapImage(mapName string) string {
	if cfg.MapImages == "" {
		return ""
	}
	name := path.Base(mapName)
	if !validMapName(name) {
		return ""
	}
	if v, ok := imageCache.Load(mapKey(name)); ok {
		if e := v.(imageEntry); e.url != "" || time.Since(e.at) < missRetry {
			return e.url
		}
	}
	// Steam sometimes refuses or times out when asked often, so retry errors (not
	// "no picture" answers) a couple of times before giving up on this card.
	var img string
	var err error
	for attempt := 0; attempt < lookupAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(lookupRetryDelay)
		}
		if img, err = findMapImage(name); err == nil {
			imageCache.Store(mapKey(name), imageEntry{img, time.Now()})
			return img
		}
	}
	slog.Warn("map picture lookup failed", "map", name, "err", err)
	return "" // not cached, so the next card tries again
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
	if base := baseMapName(name); base != strings.ToLower(name) {
		tries = append(tries, base)
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
	Views      int    `json:"views"`
}

// titleMatch scores how well a Workshop title names the map: 2 exact (any case), 1 the
// map's letters and digits appear in the title as whole words, whatever separates
// them, 0 no match. So ze_amdaporkeep matches "ZE_AMDAPOR_KEEP", and
// ze_bang_dream_mygo matches "[BangDream][MyGO]ze_bang_dream_mygo", while ze_x doesn't
// match "aze_x" or "ze_xyz".
func titleMatch(title, name string) int {
	if strings.EqualFold(strings.TrimSpace(title), name) {
		return 2
	}
	want := strings.Join(words(name), "")
	if want == "" {
		return 0
	}
	tw := words(title)
	for i := range tw { // join runs of whole title words and compare
		joined := ""
		for j := i; j < len(tw) && len(joined) < len(want); j++ {
			if joined += tw[j]; joined == want {
				return 1
			}
		}
	}
	return 0
}

// words splits s into lowercase runs of letters and digits.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// versionSuffix matches the tags servers append to map names: "_p" (patched),
// "_v2", "_b3", "_fix", "_final", "_cs2", single letters like "_t", and similar.
var versionSuffix = regexp.MustCompile(`^(?:[a-z]|v\d+[a-z]?|b\d+|rc\d*|fix(?:ed)?\d*|final\d*|cs2|csgo|beta\d*|alpha\d*|test\d*)$`)

// baseMapName strips version suffixes: zm_420_office_p -> zm_420_office,
// ze_ffxii_westersand_v7_t -> ze_ffxii_westersand. It always keeps the prefix and one
// name part, so it never reduces a map to just "ze".
func baseMapName(name string) string {
	parts := strings.Split(strings.ToLower(name), "_")
	for len(parts) > 2 && versionSuffix.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, "_")
}

// pickPreviews returns the previews of CS2 items named after the map: exact titles
// first, then most-viewed. Maps are often re-uploaded (sometimes with a blank preview);
// the original is the one people actually visit, while re-uploads collect server
// subscriptions instead. When nothing is named after the full map name, it tries the
// name without version suffixes, since the Workshop upload is often the unpatched map.
func pickPreviews(items []workshopItem, name string) []string {
	urls := pickPreviewsExact(items, name)
	if base := baseMapName(name); len(urls) == 0 && base != strings.ToLower(name) {
		urls = pickPreviewsExact(items, base)
	}
	return urls
}

func pickPreviewsExact(items []workshopItem, name string) []string {
	var matches []workshopItem
	for _, it := range items {
		if it.AppID == 730 && titleMatch(it.Title, name) > 0 && strings.HasPrefix(it.PreviewURL, "https://") {
			matches = append(matches, it)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		si, sj := titleMatch(matches[i].Title, name), titleMatch(matches[j].Title, name)
		if si != sj {
			return si > sj
		}
		return matches[i].Views > matches[j].Views
	})
	urls := make([]string, len(matches))
	for i, m := range matches {
		urls[i] = m.PreviewURL
	}
	return urls
}

const (
	// minPreviewBytes: anything smaller is too small to be a picture at all.
	minPreviewBytes = 2000
	// maxPreviewBytes caps how much of a preview is read to check it.
	maxPreviewBytes = 8 << 20
)

// realPicture reports whether a preview is an actual picture, not a blank placeholder
// (Workshop uploads without a screenshot get a plain black one). It looks at the pixels:
// file size alone misjudges simple pictures, like a dark scene that compresses to 6 KB.
// It downloads with GET because Steam's image server sometimes answers HEAD with 404.
func realPicture(u string) (bool, error) {
	resp, err := httpClient.Get(u)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPreviewBytes))
	if err != nil || len(data) < minPreviewBytes {
		return false, nil
	}
	if c, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || c.Width*c.Height > 4_000_000 {
		return true, nil // unknown format or a huge picture (too much memory to check): trust it
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return true, nil // a format we can't decode (like WebP or GIF): trust it
	}
	return !blankImage(img), nil
}

// blankImage reports whether a picture is (nearly) one flat color, by sampling a grid
// of pixels and comparing their brightness.
func blankImage(img image.Image) bool {
	b := img.Bounds()
	lo, hi := uint32(1<<16), uint32(0)
	for y := b.Min.Y; y < b.Max.Y; y += max(b.Dy()/32, 1) {
		for x := b.Min.X; x < b.Max.X; x += max(b.Dx()/32, 1) {
			r, g, bl, _ := img.At(x, y).RGBA()
			l := (r*299 + g*587 + bl*114) / 1000
			lo, hi = min(lo, l), max(hi, l)
		}
	}
	return hi-lo < 0x0c00 // under ~5% brightness range: a flat placeholder
}

// workshopPreview finds a CS2 Workshop map by name and returns its preview picture. It
// searches for the map name, then (if nothing fits) for it with spaces instead of
// underscores, since Workshop titles are written either way.
// shortcut: scrapes the public Workshop search page because Steam's search API needs a
// key; if Steam changes that page, switch to IPublishedFileService/QueryFiles with a key.
func workshopPreview(name string) (string, error) {
	queries := []string{name}
	if spaced := strings.ReplaceAll(name, "_", " "); spaced != name {
		queries = append(queries, spaced)
	}
	if base := baseMapName(name); base != strings.ToLower(name) {
		queries = append(queries, base)
	}
	for _, q := range queries {
		resp, err := httpClient.Get("https://steamcommunity.com/workshop/browse/?appid=730&browsesort=textsearch&section=readytouseitems&searchtext=" + url.QueryEscape(q))
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
		items, err := workshopItems(workshopIDs(string(page)))
		if err != nil {
			return "", err
		}
		for _, u := range pickPreviews(items, name) {
			ok, err := realPicture(u)
			if err != nil {
				return "", err
			}
			if ok {
				return u, nil
			}
		}
	}
	return "", nil
}

// workshopItems looks up Workshop items by ID with Steam's public (keyless) API.
func workshopItems(ids []string) ([]workshopItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	form := url.Values{"itemcount": {strconv.Itoa(len(ids))}}
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	resp, err := httpClient.PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Response struct {
			Items []workshopItem `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	return out.Response.Items, nil
}

// maxCollectionMaps caps how many maps one collection may bring in (POSSESSION's has 415).
const maxCollectionMaps = 1000

type collectionEntry struct {
	items []workshopItem
	at    time.Time
}

var collectionCache sync.Map // collection id -> collectionEntry

// collectionItems returns the maps in a Workshop collection, cached for missRetry.
// It returns nil (not an error) when the ID isn't a collection.
func collectionItems(id string) ([]workshopItem, error) {
	if v, ok := collectionCache.Load(id); ok {
		if e := v.(collectionEntry); time.Since(e.at) < missRetry {
			return e.items, nil
		}
	}
	form := url.Values{"collectioncount": {"1"}, "publishedfileids[0]": {id}}
	resp, err := httpClient.PostForm("https://api.steampowered.com/ISteamRemoteStorage/GetCollectionDetails/v1/", form)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Response struct {
			Details []struct {
				Children []struct {
					ID string `json:"publishedfileid"`
				} `json:"children"`
			} `json:"collectiondetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, err
	}
	var ids []string
	if len(out.Response.Details) == 1 {
		for _, c := range out.Response.Details[0].Children {
			if len(ids) < maxCollectionMaps {
				ids = append(ids, c.ID)
			}
		}
	}
	// Batches of 100, fetched in parallel: 415 maps one after another took ~9.5s.
	var batches [][]string
	for i := 0; i < len(ids); i += 100 {
		batches = append(batches, ids[i:min(i+100, len(ids))])
	}
	results := make([][]workshopItem, len(batches))
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	for i, b := range batches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = workshopItems(b)
		}()
	}
	wg.Wait()
	var items []workshopItem
	for i := range batches {
		if errs[i] != nil {
			return nil, errs[i]
		}
		items = append(items, results[i]...)
	}
	collectionCache.Store(id, collectionEntry{items, time.Now()})
	return items, nil
}

// collectionPicture finds a map's picture in a linked collection, cached like mapImage.
func collectionPicture(collectionID, mapName string) string {
	key := "c:" + collectionID + ":" + mapKey(mapName)
	if v, ok := imageCache.Load(key); ok {
		if e := v.(imageEntry); e.url != "" || time.Since(e.at) < missRetry {
			return e.url
		}
	}
	items, err := collectionItems(collectionID)
	if err != nil {
		return ""
	}
	for _, u := range pickPreviews(items, path.Base(mapName)) {
		ok, err := realPicture(u)
		if err != nil {
			return "" // network hiccup: not cached
		}
		if ok {
			imageCache.Store(key, imageEntry{u, time.Now()})
			return u
		}
	}
	imageCache.Store(key, imageEntry{"", time.Now()})
	return ""
}

var workshopLinkRe = regexp.MustCompile(`^https://steamcommunity\.com/(?:sharedfiles|workshop)/filedetails/\?(?:.*&)?id=(\d+)`)

// pictureFromLink turns what an admin pasted into a picture URL: a CS2 Workshop page
// becomes its preview picture, a plain https link is used as is. ok is false for
// anything else.
func pictureFromLink(link string) (string, bool, error) {
	link = strings.TrimSpace(link)
	if m := workshopLinkRe.FindStringSubmatch(link); m != nil {
		items, err := workshopItems([]string{m[1]})
		if err != nil {
			return "", false, err
		}
		if len(items) == 1 && items[0].AppID == 730 && strings.HasPrefix(items[0].PreviewURL, "https://") {
			return items[0].PreviewURL, true, nil
		}
		return "", false, nil
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(link) > 500 {
		return "", false, nil
	}
	return link, true, nil
}
