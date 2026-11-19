package main

// notifylist.go: /notify list, laid out like /servers list: numbered rows with an Edit
// button each (change the map, where it applies, or remove it), paging, Remove all,
// and a button to switch between DM and channel alerts.
// Custom IDs are "ntf:<action>:<alert id>:<page>". It's private to the user, and every
// change goes through user-scoped queries, so nobody can touch someone else's alerts.

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

const alertPageSize = 5

func alertID(action string, id int64, page int) string {
	return fmt.Sprintf("ntf:%s:%d:%d", action, id, page)
}

func box(parts ...discordgo.MessageComponent) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(colorShiron), Components: parts}}
}

// alertListView is one page of a user's alerts in a guild. notice, when set, is a
// result line shown on top (like an error from an edit).
func alertListView(loc, user, guild string, page int, notice string) []discordgo.MessageComponent {
	list, err := db.Notifies(user, guild)
	if err != nil {
		return []discordgo.MessageComponent{textV2("❌ " + T(loc, "err.internal"))}
	}
	if notice != "" {
		notice += "\n"
	}
	mode := db.Delivery(user)
	other := "channel"
	if mode == "channel" {
		other = "dm"
	}
	switchBtn := discordgo.Button{Label: T(loc, "notify.switch", T(loc, "mode."+other)), Style: discordgo.SecondaryButton,
		CustomID: alertID("mode", 0, page)}
	if len(list) == 0 {
		return box(textV2(notice+T(loc, "notify.none", T(loc, "mode."+mode))),
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{switchBtn}})
	}
	pages := (len(list) + alertPageSize - 1) / alertPageSize
	page = max(0, min(page, pages-1))
	parts := []discordgo.MessageComponent{
		textV2(notice + "### " + T(loc, "notify.list_title", len(list)) + "\n-# " + T(loc, "notify.list_hint", T(loc, "mode."+mode), page+1, pages)),
	}
	for i := page * alertPageSize; i < min((page+1)*alertPageSize, len(list)); i++ {
		n := list[i]
		parts = append(parts, discordgo.Section{
			Components: []discordgo.MessageComponent{textV2(fmt.Sprintf("`#%d` %s\n-# %s", i+1, code(n.Pattern), notifyWhere(loc, n)))},
			Accessory:  discordgo.Button{Label: T(loc, "button.edit"), Style: discordgo.SecondaryButton, CustomID: alertID("open", n.ID, page)},
		})
	}
	var row []discordgo.MessageComponent
	if pages > 1 {
		row = append(row,
			discordgo.Button{Label: "◀", Style: discordgo.SecondaryButton, CustomID: alertID("page", 0, page-1), Disabled: page == 0},
			discordgo.Button{Label: fmt.Sprintf("%d / %d", page+1, pages), Style: discordgo.SecondaryButton, CustomID: "ntf:noop", Disabled: true},
			discordgo.Button{Label: "▶", Style: discordgo.SecondaryButton, CustomID: alertID("page", 1, page+1), Disabled: page == pages-1},
		)
	}
	row = append(row, switchBtn,
		discordgo.Button{Label: T(loc, "notify.remove_all"), Style: discordgo.DangerButton, CustomID: alertID("rmall", 0, page)})
	parts = append(parts, discordgo.ActionsRow{Components: row}) // at most 5, Discord's limit
	return box(parts...)
}

// alertEditView is the Edit panel for one alert: where it applies, change map, remove.
func alertEditView(loc, guild string, n Notify, num, page int) []discordgo.MessageComponent {
	servers, _ := db.Servers(guild)
	// A dropdown holds 25 choices: "all servers" plus up to 24 servers, always
	// including the one this alert is on.
	if len(servers) > 24 {
		keep := servers[:24]
		for _, sv := range servers[24:] {
			if sv.ID == n.Server.ID {
				keep[23] = sv
			}
		}
		servers = keep
	}
	options := []discordgo.SelectMenuOption{{Label: truncate(T(loc, "notify.all_servers"), 100), Value: "0", Default: n.AllServers(), Emoji: &discordgo.ComponentEmoji{Name: "🌐"}}}
	for _, sv := range servers {
		options = append(options, discordgo.SelectMenuOption{Label: truncate(sv.displayName(), 100), Description: truncate(sv.Address, 100),
			Value: strconv.FormatInt(sv.ID, 10), Default: sv.ID == n.Server.ID})
	}
	return box(
		textV2(fmt.Sprintf("### `#%d` %s\n-# %s", num, code(n.Pattern), notifyWhere(loc, n))),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu,
			CustomID: alertID("where", n.ID, page), Placeholder: T(loc, "notify.where"), Options: options}}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: T(loc, "panel.back"), Style: discordgo.SecondaryButton, CustomID: alertID("page", n.ID, page)},
			discordgo.Button{Label: T(loc, "notify.change_map"), Style: discordgo.SecondaryButton, CustomID: alertID("map", n.ID, page), Emoji: &discordgo.ComponentEmoji{Name: "✏️"}},
			discordgo.Button{Label: T(loc, "panel.remove"), Style: discordgo.DangerButton, CustomID: alertID("rm", n.ID, page)},
		}},
	)
}

// findAlert returns one of the user's alerts in this guild and its list number.
func findAlert(user, guild string, id int64) (Notify, int, bool) {
	list, _ := db.Notifies(user, guild)
	for i, n := range list {
		if n.ID == id {
			return n, i + 1, true
		}
	}
	return Notify{}, 0, false
}

// cleanPattern checks a typed map name or wildcard ("de_dust2", "ze_*").
func cleanPattern(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if _, err := path.Match(p, ""); err != nil || p == "" || len(p) > 64 {
		return "", errors.New("bad pattern")
	}
	return p, nil
}

func cmdNotifyList(c *ictx) { c.respondV2(true, alertListView(c.loc, c.user(), c.guild(), 0, "")...) }

// onAlertList handles every click and form in /notify list.
func onAlertList(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := &ictx{s: s, i: i, loc: string(i.Locale)}
	var customID string
	var values []string
	if i.Type == discordgo.InteractionModalSubmit {
		customID = i.ModalSubmitData().CustomID
	} else {
		d := i.MessageComponentData()
		customID, values = d.CustomID, d.Values
	}
	parts := strings.Split(customID, ":")
	if len(parts) != 4 || c.guild() == "" {
		return
	}
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	page, _ := strconv.Atoi(parts[3])
	user, guild := c.user(), c.guild()
	list := func(notice string) { c.update(alertListView(c.loc, user, guild, page, notice)) }

	switch parts[1] {
	case "page":
		list("")
	case "mode":
		next := "channel"
		if db.Delivery(user) == "channel" {
			next = "dm"
		}
		if err := db.SetDelivery(user, next); err != nil {
			c.fail("err.internal")
			return
		}
		list("")
	case "rmall":
		n, _ := db.Notifies(user, guild)
		c.update(box(textV2(T(c.loc, "notify.confirm_all", len(n))), discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: T(c.loc, "panel.confirm_yes"), Style: discordgo.DangerButton, CustomID: alertID("rmallyes", 0, page)},
			discordgo.Button{Label: T(c.loc, "panel.cancel"), Style: discordgo.SecondaryButton, CustomID: alertID("page", 0, page)},
		}}))
	case "rmallyes":
		if err := db.RemoveAllNotifies(user, guild); err != nil {
			c.fail("err.internal")
			return
		}
		forgetDMUser(user)
		list("")
	case "rm":
		if _, err := db.RemoveNotify(user, id); err != nil { // user-scoped: only your own
			c.fail("err.internal")
			return
		}
		list("")
	default: // actions on one alert
		n, num, ok := findAlert(user, guild, id)
		if !ok {
			list("")
			return
		}
		switch parts[1] {
		case "open":
			c.update(alertEditView(c.loc, guild, n, num, page))
		case "where":
			if len(values) != 1 {
				return
			}
			sid, _ := strconv.ParseInt(values[0], 10, 64)
			if sid != 0 {
				if _, err := db.Server(guild, sid); err != nil { // only servers tracked here
					return
				}
			}
			c.saveAlert(n, sid, n.Pattern, num, page)
		case "map":
			c.openForm(alertID("mapset", n.ID, page), T(c.loc, "notify.change_map"),
				discordgo.TextInput{CustomID: "map", Label: T(c.loc, "form.alert_map"), Style: discordgo.TextInputShort,
					Value: n.Pattern, Required: true, MaxLength: 64})
		case "mapset":
			p, err := cleanPattern(formValues(i.ModalSubmitData())["map"])
			if err != nil {
				c.fail("err.bad_pattern")
				return
			}
			c.saveAlert(n, n.Server.ID, p, num, page)
		}
	}
}

// saveAlert stores an edited alert and shows its panel again with the change.
func (c *ictx) saveAlert(n Notify, serverID int64, pattern string, num, page int) {
	switch _, err := db.EditNotify(c.user(), n.ID, serverID, pattern); {
	case isUnique(err):
		c.fail("err.already_notify", code(pattern))
		return
	case err != nil:
		c.fail("err.internal")
		return
	}
	updated, _, _ := findAlert(c.user(), c.guild(), n.ID)
	c.update(alertEditView(c.loc, c.guild(), updated, num, page))
}
