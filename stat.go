package main

// stat.go: /stat, a server's player history from the samples the poller saves every 5
// minutes. A line chart picture (drawn here with the standard library, no text inside
// so every language works), four numbers, a map dropdown standing in for hover, range
// buttons, and a "busy hours" view showing when the server is full, by day and hour.
// Custom IDs: "stat:<24h|7d|30d|busy>:<server id>" for buttons and
// "stat:map:<server id>:<range>" for the map dropdown (value = the map's start time).

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

var statRanges = map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": statKeep}

// minStatSamples: less history than this (half an hour) isn't worth a chart.
const minStatSamples = 6

// mapRun is one map as it was played: from its first sample to just after its last.
type mapRun struct {
	Map        string
	Start, End int64
	Avg, Most  int
	total, n   int // for the average
}

type statSummary struct {
	Avg      int
	Full     time.Duration // time with 90%+ of slots taken
	BusyHour int64         // a recent time in the busiest hour of the day; 0 = none
	Runs     []mapRun      // oldest first
	Max      int           // most slots seen
}

// summarize works out /stat's numbers from samples taken step seconds apart.
func summarize(samples []Sample, step int64) statSummary {
	var sum statSummary
	var total, online int
	var hourSum, hourN [24]int
	var lastAt int64
	for _, s := range samples {
		if s.Players < 0 {
			continue
		}
		online++
		total += s.Players
		sum.Max = max(sum.Max, s.Max)
		if s.Max > 0 && s.Players*10 >= s.Max*9 {
			sum.Full += time.Duration(step) * time.Second
		}
		h := (s.At / 3600) % 24
		hourSum[h] += s.Players
		hourN[h]++
		lastAt = s.At
		if n := len(sum.Runs); n == 0 || sum.Runs[n-1].Map != s.Map {
			sum.Runs = append(sum.Runs, mapRun{Map: s.Map, Start: s.At})
		}
		r := &sum.Runs[len(sum.Runs)-1]
		r.End = s.At + step
		r.total += s.Players
		r.n++
		r.Avg = (r.total + r.n/2) / r.n
		r.Most = max(r.Most, s.Players)
	}
	if online == 0 {
		return sum
	}
	sum.Avg = (total + online/2) / online
	best := -1
	for h := range hourN {
		if hourN[h] > 0 && (best < 0 || hourSum[h]*hourN[best] > hourSum[best]*hourN[h]) {
			best = h
		}
	}
	// The latest time that hour came round: <t:…:t> then shows it in each reader's zone.
	sum.BusyHour = lastAt/3600*3600 - ((lastAt/3600%24-int64(best)+24)%24)*3600
	return sum
}

// ---------- chart picture ----------

const chartW, chartH, chartPad = 800, 240, 8

var (
	chartBG      = color.RGBA{0x1e, 0x1f, 0x22, 0xff}
	chartGrid    = color.RGBA{0x2c, 0x2d, 0x31, 0xff}
	chartOffline = color.RGBA{0x3a, 0x3c, 0x42, 0xff}
	chartPick    = color.RGBA{0x87, 0x87, 0xff, 0x38}
	chartReal    = color.RGBA{0x5f, 0xd7, 0x87, 0xff}
	chartArea    = color.RGBA{0x5f, 0xd7, 0x87, 0x1c}
	chartFake    = color.RGBA{0xff, 0xd7, 0x5f, 0xff}
)

type bucket struct {
	real, rep, n, off int
}

// drawChart draws players from `from` to `to`: green line and area for real players,
// dashed yellow where the server reported more, gray where it was offline, and a purple
// band over the picked map. Times without any sample stay empty.
func drawChart(samples []Sample, from, to, step int64, slots int, pick *mapRun) []byte {
	img := image.NewRGBA(image.Rect(0, 0, chartW, chartH))
	fill(img, 0, 0, chartW, chartH, chartBG)
	slots = max(slots, 1)
	y := func(v int) int { return chartH - chartPad - min(v, slots)*(chartH-2*chartPad)/slots }
	for q := 1; q <= 4; q++ {
		fill(img, 0, y(slots*q/4), chartW, y(slots*q/4)+1, chartGrid)
	}
	// Buckets at least one sample wide, so the line has no holes between samples.
	width := max(step, (to-from)/chartW+1)
	n := int((to-from)/width) + 1
	b := make([]bucket, n)
	for _, s := range samples {
		i := int((s.At - from) / width)
		if i < 0 || i >= n {
			continue
		}
		if s.Players < 0 {
			b[i].off++
			continue
		}
		b[i].real += s.Players
		b[i].rep += s.Reported
		b[i].n++
	}
	x := func(i int) int { return int(int64(i) * width * chartW / (to - from)) }
	if pick != nil {
		x0, x1 := int((pick.Start-from)*chartW/(to-from)), int((pick.End-from)*chartW/(to-from))
		fill(img, x0, 0, max(x1, x0+2), chartH, chartPick)
	}
	for i := range b {
		if b[i].n == 0 && b[i].off > 0 {
			fill(img, x(i), 0, max(x(i+1), x(i)+1), chartH, chartOffline)
		}
	}
	avg := func(sum, n int) int { return (sum + n/2) / n }
	for i := 1; i < n; i++ {
		p, c := b[i-1], b[i]
		if p.n == 0 || c.n == 0 {
			continue
		}
		pr, cr := avg(p.real, p.n), avg(c.real, c.n)
		for xx := x(i - 1); xx < x(i); xx++ { // area under the line
			v := pr + (cr-pr)*(xx-x(i-1))/max(x(i)-x(i-1), 1)
			fill(img, xx, y(v), xx+1, chartH-chartPad, chartArea)
		}
		line(img, x(i-1), y(pr), x(i), y(cr), chartReal, false)
		if pf, cf := avg(p.rep, p.n), avg(c.rep, c.n); pf > pr+fakeSlack && cf > cr+fakeSlack {
			line(img, x(i-1), y(pf), x(i), y(cf), chartFake, true)
		}
	}
	var out bytes.Buffer
	png.Encode(&out, img)
	return out.Bytes()
}

// fill blends c over the rectangle [x0,x1)×[y0,y1).
func fill(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	r := image.Rect(x0, y0, x1, y1).Intersect(img.Bounds())
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		for xx := r.Min.X; xx < r.Max.X; xx++ {
			if c.A == 0xff {
				img.SetRGBA(xx, yy, c)
				continue
			}
			o := img.RGBAAt(xx, yy)
			a := uint32(c.A)
			mix := func(f, b uint8) uint8 { return uint8((uint32(f)*a + uint32(b)*(255-a)) / 255) }
			img.SetRGBA(xx, yy, color.RGBA{mix(c.R, o.R), mix(c.G, o.G), mix(c.B, o.B), 0xff})
		}
	}
}

// line draws a 2-pixel line, dashed if asked (6 on, 6 off along x).
func line(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA, dashed bool) {
	steps := max(abs(x1-x0), abs(y1-y0), 1)
	for k := 0; k <= steps; k++ {
		xx, yy := x0+(x1-x0)*k/steps, y0+(y1-y0)*k/steps
		if dashed && xx/6%2 == 1 {
			continue
		}
		fill(img, xx, yy-1, xx+2, yy+1, c)
	}
}

func abs(v int) int { return max(v, -v) }

// ---------- busy hours ----------

// zoneFor guesses a reader's time zone from their Discord language, for the busy-hours
// grid (Discord doesn't tell bots a user's zone, and the grid can't use <t:…> tags).
func zoneFor(loc string) (*time.Location, string) {
	switch {
	case strings.HasPrefix(loc, "th"):
		return time.FixedZone("UTC+7", 7*3600), "UTC+7"
	case strings.HasPrefix(loc, "zh"):
		return time.FixedZone("UTC+8", 8*3600), "UTC+8"
	}
	return time.UTC, "UTC"
}

var heatScale = []string{"⬛", "🟦", "🟩", "🟨", "🟧", "🟥"}

// busyView is the busy-hours grid: one row per weekday, one square per 2 hours, colored
// by the average share of slots taken, plus the best and quietest times.
func busyView(loc string, samples []Sample) string {
	zone, zoneName := zoneFor(loc)
	var sum, n, slots [7][12]int
	for _, s := range samples {
		if s.Players < 0 || s.Max <= 0 {
			continue
		}
		t := time.Unix(s.At, 0).In(zone)
		d, h := (int(t.Weekday())+6)%7, t.Hour()/2 // Monday first
		sum[d][h] += s.Players
		slots[d][h] += s.Max
		n[d][h]++
	}
	days := strings.Split(T(loc, "stat.days"), ",")
	var b strings.Builder
	type cell struct{ d, h, avg int }
	var best, quiet *cell
	for d := 0; d < 7; d++ {
		fmt.Fprintf(&b, "`%-3s` ", days[d])
		for h := 0; h < 12; h++ {
			if n[d][h] == 0 {
				b.WriteString("▫️")
				continue
			}
			share := sum[d][h] * 100 / slots[d][h]
			b.WriteString(heatScale[min(share/18, len(heatScale)-1)])
			c := cell{d, h, (sum[d][h] + n[d][h]/2) / n[d][h]}
			if best == nil || c.avg > best.avg {
				best = &c
			}
			if quiet == nil || c.avg < quiet.avg {
				quiet = &c
			}
		}
		b.WriteString("\n")
	}
	if best == nil {
		return T(loc, "stat.collecting")
	}
	when := func(c *cell) string { return fmt.Sprintf("%s %02d:00–%02d:00", days[c.d], c.h*2, c.h*2+2) }
	b.WriteString("-# " + T(loc, "stat.busy_scale") + "\n-# " + T(loc, "stat.busy_note", zoneName) + "\n")
	b.WriteString(T(loc, "stat.busy_best", when(best), best.avg) + "\n-# " + T(loc, "stat.busy_quiet", when(quiet), quiet.avg))
	return b.String()
}

// ---------- the message ----------

// statView builds /stat's message for a range ("24h", "7d", "30d" or "busy") and an
// optional picked map (its start time). The returned file is the chart, if any.
func statView(loc string, sv Server, view string, pick int64, now time.Time) ([]discordgo.MessageComponent, *discordgo.File) {
	step := int64(sampleEvery / time.Second)
	span, ok := statRanges[view]
	if !ok {
		span = statKeep // busy hours look at everything kept
	}
	from := now.Add(-span).Unix()
	samples, err := db.Samples(sv.Address, from)
	if err != nil {
		slog.Error("read player history", "err", err)
		return []discordgo.MessageComponent{textV2("❌ " + T(loc, "err.internal"))}, nil
	}
	rangeKey := view
	if view == "busy" {
		rangeKey = "30d"
	}
	head := "## " + plain(sv.displayName()) + "\n-# " + T(loc, "stat.range."+rangeKey) + " · " + code(sv.Address)
	parts := []discordgo.MessageComponent{textV2(head)}
	var file *discordgo.File
	if len(samples) < minStatSamples {
		parts = append(parts, textV2(T(loc, "stat.collecting")))
	} else if view == "busy" {
		parts = append(parts, textV2(busyView(loc, samples)))
	} else {
		sum := summarize(samples, step)
		var picked *mapRun
		for i := range sum.Runs {
			if sum.Runs[i].Start == pick {
				picked = &sum.Runs[i]
			}
		}
		avgLen := time.Duration(0)
		for _, r := range sum.Runs {
			avgLen += time.Duration(r.End-r.Start) * time.Second
		}
		if len(sum.Runs) > 0 {
			avgLen /= time.Duration(len(sum.Runs))
		}
		tiles := T(loc, "stat.tiles", sum.Avg, span0(sum.Full), len(sum.Runs), span0(avgLen))
		if sum.BusyHour > 0 {
			tiles += "  ·  " + T(loc, "stat.busiest", sum.BusyHour)
		}
		file = &discordgo.File{Name: "stat.png", ContentType: "image/png",
			Reader: bytes.NewReader(drawChart(samples, from, now.Unix(), step, sum.Max, picked))}
		parts = append(parts, textV2(tiles),
			discordgo.MediaGallery{Items: []discordgo.MediaGalleryItem{{Media: discordgo.UnfurledMediaItem{URL: "attachment://stat.png"}}}},
			textV2("-# "+T(loc, "stat.span", from, sum.Max)+"\n-# "+T(loc, "stat.legend")))
		if picked != nil {
			parts = append(parts, textV2(T(loc, "stat.map_detail", plain(picked.Map), picked.Start, span0(time.Duration(picked.End-picked.Start)*time.Second), picked.Avg, picked.Most)))
		}
		if menu := mapMenu(loc, sv, view, sum.Runs, pick, span > 24*time.Hour); menu != nil {
			parts = append(parts, discordgo.ActionsRow{Components: []discordgo.MessageComponent{*menu}})
		}
	}
	var row []discordgo.MessageComponent
	for _, v := range []string{"24h", "7d", "30d", "busy"} {
		style := discordgo.SecondaryButton
		if v == view {
			style = discordgo.PrimaryButton
		}
		row = append(row, discordgo.Button{Label: T(loc, "stat.button."+v), Style: style,
			CustomID: "stat:" + v + ":" + strconv.FormatInt(sv.ID, 10)})
	}
	parts = append(parts, discordgo.ActionsRow{Components: row})
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(colorShiron), Components: parts}}, file
}

// mapMenu lists the newest maps played (Discord allows 25 options). Option text can't
// use <t:…> tags, so times are in the zone guessed from the reader's language.
func mapMenu(loc string, sv Server, view string, runs []mapRun, pick int64, withDate bool) *discordgo.SelectMenu {
	if len(runs) == 0 {
		return nil
	}
	zone, _ := zoneFor(loc)
	layout := "15:04"
	if withDate {
		layout = "Jan 2 15:04"
	}
	var opts []discordgo.SelectMenuOption
	for i := len(runs) - 1; i >= 0 && len(opts) < 25; i-- {
		r := runs[i]
		opts = append(opts, discordgo.SelectMenuOption{
			Label:       truncate(r.Map, 100),
			Value:       strconv.FormatInt(r.Start, 10),
			Description: truncate(T(loc, "stat.map_option", time.Unix(r.Start, 0).In(zone).Format(layout), span0(time.Duration(r.End-r.Start)*time.Second), r.Avg), 100),
			Default:     r.Start == pick,
		})
	}
	return &discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, Placeholder: T(loc, "stat.pick_map"), Options: opts,
		CustomID: "stat:map:" + strconv.FormatInt(sv.ID, 10) + ":" + view}
}

// span0 is downtime() with "0m" for nothing at all.
func span0(d time.Duration) string {
	if d <= 0 {
		return "0m"
	}
	return downtime(d)
}

func cmdStat(c *ictx) {
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return
	}
	comps, file := statView(c.loc, *sv, "24h", 0, time.Now())
	data := &discordgo.InteractionResponseData{Components: comps, Flags: discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if file != nil {
		data.Files = []*discordgo.File{file}
	}
	if err := c.s.InteractionRespond(c.i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data}); err != nil {
		slog.Warn("stat respond failed", "err", err)
	}
}

// onStat handles /stat's range buttons and map dropdown. The message is shared, so a
// click changes it for everyone, like flipping a dashboard.
func onStat(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := &ictx{s: s, i: i, loc: string(i.Locale)}
	d := i.MessageComponentData()
	parts := strings.Split(d.CustomID, ":")
	if len(parts) < 3 || c.guild() == "" {
		return
	}
	view, pick := parts[1], int64(0)
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	if view == "map" {
		if len(parts) != 4 || len(d.Values) != 1 {
			return
		}
		view = parts[3]
		pick, _ = strconv.ParseInt(d.Values[0], 10, 64)
	}
	if _, ok := statRanges[view]; !ok && view != "busy" {
		return
	}
	sv, err := db.Server(c.guild(), id) // only servers tracked in this Discord server
	if err != nil {
		c.fail("err.server_not_found")
		return
	}
	comps, file := statView(c.loc, *sv, view, pick, time.Now())
	data := &discordgo.InteractionResponseData{Components: comps, Flags: discordgo.MessageFlagsIsComponentsV2,
		AllowedMentions: &discordgo.MessageAllowedMentions{}, Attachments: &[]*discordgo.MessageAttachment{}}
	if file != nil {
		// Keep only the new picture (attachment "0" is the first uploaded file).
		data.Files = []*discordgo.File{file}
		data.Attachments = &[]*discordgo.MessageAttachment{{ID: "0", Filename: file.Name}}
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: data}); err != nil {
		slog.Warn("stat update failed", "err", err)
	}
}
