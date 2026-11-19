package main

// manage.go: /servers list (numbered rows with a Manage button each, paged) and the
// private Manage panel (style, minimum players, channel, remove).
//
// Custom IDs are "srvm:<action>:<server id>:<page>" so every click knows which server
// and which list page it belongs to.

import (
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const listPageSize = 5

// serverDot is the status dot for a server from its last check, plus that check's
// result when it's recent: 🟢 busy, 🟡 a few players, ⚫ empty, 🔴 offline, ⚪ not
// checked yet (just after a restart).
func serverDot(addr string) (string, *Status) {
	t, ok := lastSeen.Load(addr)
	if !ok {
		return "⚪", nil
	}
	if time.Since(t.(time.Time)) > offlineAfter*cfg.Poll+30*time.Second {
		return "🔴", nil
	}
	v, _ := lastStatus.Load(addr)
	st, _ := v.(*Status)
	if st == nil {
		return "⚪", nil
	}
	switch accent(st) {
	case colorBusy:
		return "🟢", st
	case colorQuiet:
		return "🟡", st
	}
	return "⚫", st
}

func (s Server) displayName() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Address
}

// serverLine is one row of the list: number, dot, map and players, then where it posts.
func serverLine(loc string, n int, sv Server) string {
	dot, st := serverDot(sv.Address)
	var first string
	switch {
	case st != nil:
		first = fmt.Sprintf("**%s** · %d/%d", plain(st.Map), st.Humans(), st.Max)
	case dot == "🔴":
		first = "**" + T(loc, "list.offline") + "**"
	default:
		first = "**" + plain(sv.LastMap) + "**"
	}
	second := plain(truncate(sv.displayName(), 40)) + " · <#" + sv.ChannelID + "> · " + T(loc, "style."+sv.Style)
	if sv.MinPlayers > 0 {
		second += " · " + T(loc, "list.min", sv.MinPlayers)
	}
	if sv.Paused {
		second += "\n⚠️ " + T(loc, "list.paused", sv.ChannelID)
	}
	return fmt.Sprintf("`#%d` %s %s\n-# %s", n, dot, first, second)
}

func manageID(action string, id int64, page int) string {
	return fmt.Sprintf("srvm:%s:%d:%d", action, id, page)
}

// listView is the /servers list message for one page.
func listView(loc, guild string, page int) []discordgo.MessageComponent {
	return listViewWith(loc, guild, page, "")
}

// listViewWith is listView with a result line (like "✅ Linked a collection…") on top.
func listViewWith(loc, guild string, page int, notice string) []discordgo.MessageComponent {
	list, err := db.Servers(guild)
	if err != nil {
		return []discordgo.MessageComponent{textV2("❌ " + T(loc, "err.internal"))}
	}
	if notice != "" {
		notice += "\n"
	}
	// Map pictures and Collections work before any server is added, so they're always there.
	tools := []discordgo.MessageComponent{
		discordgo.Button{Label: T(loc, "button.pictures"), Style: discordgo.SecondaryButton, CustomID: manageID("pic", 0, page), Emoji: &discordgo.ComponentEmoji{Name: "🖼️"}},
		discordgo.Button{Label: T(loc, "button.collections"), Style: discordgo.SecondaryButton, CustomID: manageID("col", 0, page), Emoji: &discordgo.ComponentEmoji{Name: "📚"}},
	}
	if len(list) == 0 {
		return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(colorShiron), Components: []discordgo.MessageComponent{
			textV2(notice + T(loc, "err.no_servers")),
			discordgo.ActionsRow{Components: tools},
		}}}
	}
	pages := (len(list) + listPageSize - 1) / listPageSize
	page = max(0, min(page, pages-1))
	parts := []discordgo.MessageComponent{
		textV2(notice + "### " + T(loc, "list.title", len(list)) + "\n-# " + T(loc, "list.hint", page+1, pages)),
	}
	for i := page * listPageSize; i < min((page+1)*listPageSize, len(list)); i++ {
		sv := list[i]
		parts = append(parts, discordgo.Section{
			Components: []discordgo.MessageComponent{textV2(serverLine(loc, i+1, sv))},
			Accessory:  discordgo.Button{Label: T(loc, "button.manage"), Style: discordgo.SecondaryButton, CustomID: manageID("open", sv.ID, page)},
		})
	}
	var row []discordgo.MessageComponent
	if pages > 1 {
		row = append(row,
			discordgo.Button{Label: "◀", Style: discordgo.SecondaryButton, CustomID: manageID("page", 0, page-1), Disabled: page == 0},
			discordgo.Button{Label: fmt.Sprintf("%d / %d", page+1, pages), Style: discordgo.SecondaryButton, CustomID: "srvm:noop", Disabled: true},
			discordgo.Button{Label: "▶", Style: discordgo.SecondaryButton, CustomID: manageID("page", 1, page+1), Disabled: page == pages-1},
		)
	}
	parts = append(parts, discordgo.ActionsRow{Components: append(row, tools...)}) // at most 5, Discord's limit
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(colorShiron), Components: parts}}
}

const maxCollectionsPerGuild = 5

// setMapPicture applies the Map pictures form: a Workshop page or https image for a
// map in this Discord server, or back to the automatic picture with an empty link.
// It returns the result line shown above the list.
func setMapPicture(loc, guild, mapName, link string) string {
	if !validMapName(path.Base(mapName)) {
		return "❌ " + T(loc, "err.bad_pattern")
	}
	key := mapKey(mapName)
	if link == "" {
		if err := db.SetMapPicture(guild, key, ""); err != nil {
			return "❌ " + T(loc, "err.internal")
		}
		return "✅ " + T(loc, "servers.picture_removed", code(key))
	}
	pic, ok, err := pictureFromLink(link)
	switch {
	case err != nil:
		return "❌ " + T(loc, "err.internal")
	case !ok:
		return "❌ " + T(loc, "err.bad_picture")
	}
	if err := db.SetMapPicture(guild, key, pic); err != nil {
		return "❌ " + T(loc, "err.internal")
	}
	return "✅ " + T(loc, "servers.picture_set", code(key))
}

// linkCollection applies the Collections form: link a Steam Workshop collection (a
// community's "all our maps" list) as a picture source, or clear them with no link.
func linkCollection(loc, guild, link string) string {
	if link == "" {
		if err := db.ClearCollections(guild); err != nil {
			return "❌ " + T(loc, "err.internal")
		}
		return "✅ " + T(loc, "servers.collection_cleared")
	}
	m := workshopLinkRe.FindStringSubmatch(link)
	if m == nil {
		return "❌ " + T(loc, "err.bad_collection")
	}
	if len(db.Collections(guild)) >= maxCollectionsPerGuild {
		return "❌ " + T(loc, "err.too_many_collections", maxCollectionsPerGuild)
	}
	items, err := collectionItems(m[1])
	switch {
	case err != nil:
		return "❌ " + T(loc, "err.internal")
	case len(items) == 0:
		return "❌ " + T(loc, "err.bad_collection")
	}
	if err := db.AddCollection(guild, m[1]); err != nil {
		return "❌ " + T(loc, "err.internal")
	}
	return "✅ " + T(loc, "servers.collection_added", len(items))
}

// openForm shows a Discord form (modal) with text boxes. Discord caps labels at 45
// characters and placeholders at 100.
func (c *ictx) openForm(customID, title string, fields ...discordgo.TextInput) {
	var rows []discordgo.MessageComponent
	for _, f := range fields {
		f.Label, f.Placeholder = truncate(f.Label, 45), truncate(f.Placeholder, 100)
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{f}})
	}
	err := c.s.InteractionRespond(c.i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{CustomID: customID, Title: truncate(title, 45), Components: rows}})
	if err != nil {
		slog.Warn("open form failed", "err", err)
	}
}

// position is a server's number in the list (1-based), matching /servers list.
func position(guild string, id int64) int {
	list, _ := db.Servers(guild)
	for i, sv := range list {
		if sv.ID == id {
			return i + 1
		}
	}
	return 0
}

// panelView is the Manage panel for one server.
func panelView(loc string, sv Server, page int) []discordgo.MessageComponent {
	dot, st := serverDot(sv.Address)
	status := dot + " **" + plain(sv.LastMap) + "**"
	if st != nil {
		status = fmt.Sprintf("%s **%s** · %d/%d", dot, plain(st.Map), st.Humans(), st.Max)
	} else if dot == "🔴" {
		status = dot + " **" + T(loc, "list.offline") + "**"
	}
	alerts := serverAlerts(sv)
	text := fmt.Sprintf("### `#%d` %s\n-# %s\n%s\n%s", position(sv.GuildID, sv.ID), plain(sv.displayName()), sv.Address, status,
		T(loc, "panel.settings", sv.ChannelID, T(loc, "style."+sv.Style), sv.MinPlayers, alerts))
	if sv.Paused {
		text += "\n⚠️ " + T(loc, "panel.paused", sv.ChannelID)
	}
	other := "compact"
	if sv.Style == "compact" {
		other = "full"
	}
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(colorShiron), Components: []discordgo.MessageComponent{
		textV2(text),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{MenuType: discordgo.ChannelSelectMenu, CustomID: manageID("chan", sv.ID, page),
				Placeholder:  T(loc, "panel.channel"),
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: T(loc, "panel.back"), Style: discordgo.SecondaryButton, CustomID: manageID("page", sv.ID, page)},
			discordgo.Button{Label: T(loc, "panel.style", T(loc, "style."+other)), Style: discordgo.SecondaryButton, CustomID: manageID("style", sv.ID, page)},
			discordgo.Button{Label: T(loc, "panel.min"), Style: discordgo.SecondaryButton, CustomID: manageID("min", sv.ID, page)},
			discordgo.Button{Label: T(loc, "panel.remove"), Style: discordgo.DangerButton, CustomID: manageID("rm", sv.ID, page)},
		}},
	}}}
}

// serverAlerts counts the alerts tied to this one server (the ones Remove deletes);
// all-server alerts stay and keep covering the guild's other servers.
func serverAlerts(sv Server) int {
	all, _ := db.NotifiesFor(sv)
	n := 0
	for _, a := range all {
		if !a.AllServers() {
			n++
		}
	}
	return n
}

// confirmView asks before removing, since it also deletes everyone's alerts for it.
func confirmView(loc string, sv Server, page int) []discordgo.MessageComponent {
	alerts := serverAlerts(sv)
	return []discordgo.MessageComponent{discordgo.Container{AccentColor: ptr(0xED4245), Components: []discordgo.MessageComponent{
		textV2(T(loc, "panel.confirm", plain(sv.displayName()), alerts)),
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: T(loc, "panel.confirm_yes"), Style: discordgo.DangerButton, CustomID: manageID("rmyes", sv.ID, page)},
			discordgo.Button{Label: T(loc, "panel.cancel"), Style: discordgo.SecondaryButton, CustomID: manageID("open", sv.ID, page)},
		}},
	}}}
}

// update replaces the message the click came from (the list stays one private message).
func (c *ictx) update(comps []discordgo.MessageComponent) {
	err := c.s.InteractionRespond(c.i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Components: comps, Flags: discordgo.MessageFlagsIsComponentsV2,
			AllowedMentions: &discordgo.MessageAllowedMentions{}}})
	if err != nil {
		slog.Warn("update failed", "err", err)
	}
}

func cmdServersList(c *ictx) { c.respondV2(true, listView(c.loc, c.guild(), 0)...) }

// formValues reads a submitted form's text boxes by their custom ID.
func formValues(d discordgo.ModalSubmitInteractionData) map[string]string {
	out := map[string]string{}
	for _, comp := range d.Components {
		if row, ok := comp.(*discordgo.ActionsRow); ok {
			for _, inner := range row.Components {
				if in, ok := inner.(*discordgo.TextInput); ok {
					out[in.CustomID] = strings.TrimSpace(in.Value)
				}
			}
		}
	}
	return out
}

// onManage handles every click in the list and the Manage panel. The list is private,
// but the permission is still checked on every click, not only when it was opened.
func onManage(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := &ictx{s: s, i: i, loc: string(i.Locale)}
	if i.Member == nil || i.Member.Permissions&discordgo.PermissionManageGuild == 0 {
		c.fail("err.not_admin")
		return
	}
	var customID string
	var values []string
	if i.Type == discordgo.InteractionModalSubmit {
		customID = i.ModalSubmitData().CustomID
	} else {
		d := i.MessageComponentData()
		customID, values = d.CustomID, d.Values
	}
	parts := strings.Split(customID, ":")
	if len(parts) != 4 {
		return
	}
	action := parts[1]
	id, _ := strconv.ParseInt(parts[2], 10, 64)
	page, _ := strconv.Atoi(parts[3])
	switch action { // list-wide actions, no server involved
	case "page":
		c.update(listView(c.loc, c.guild(), page))
		return
	case "pic":
		c.openForm(manageID("picset", 0, page), T(c.loc, "button.pictures"),
			discordgo.TextInput{CustomID: "map", Label: T(c.loc, "form.picture_map"), Style: discordgo.TextInputShort, Required: true, MaxLength: 64, Placeholder: "ze_amdaporkeep"},
			discordgo.TextInput{CustomID: "link", Label: T(c.loc, "form.picture_link"), Style: discordgo.TextInputShort, MaxLength: 500,
				Placeholder: "https://steamcommunity.com/sharedfiles/filedetails/?id=…"})
		return
	case "col":
		c.openForm(manageID("colset", 0, page), T(c.loc, "button.collections"),
			discordgo.TextInput{CustomID: "link", Label: T(c.loc, "form.collection_link"), Style: discordgo.TextInputShort, MaxLength: 500,
				Placeholder: T(c.loc, "form.collection_hint", len(db.Collections(c.guild())))})
		return
	case "picset", "colset":
		// A Workshop lookup can take seconds, longer than Discord waits for an answer.
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
			slog.Warn("defer update failed", "err", err)
			return
		}
		v := formValues(i.ModalSubmitData())
		var notice string
		if action == "picset" {
			notice = setMapPicture(c.loc, c.guild(), v["map"], v["link"])
		} else {
			notice = linkCollection(c.loc, c.guild(), v["link"])
		}
		c.editV2(listViewWith(c.loc, c.guild(), page, notice)...)
		return
	}
	sv, err := db.Server(c.guild(), id) // also proves the server belongs to this guild
	if err != nil {
		c.update(listView(c.loc, c.guild(), page))
		return
	}
	switch action {
	case "open":
		c.update(panelView(c.loc, *sv, page))
	case "style":
		next := "compact"
		if sv.Style == "compact" {
			next = "full"
		}
		if _, err := db.SetStyle(c.guild(), sv.ID, next); err != nil {
			c.fail("err.internal")
			return
		}
		sv.Style = next
		c.update(panelView(c.loc, *sv, page))
	case "min":
		c.openForm(manageID("minset", sv.ID, page), T(c.loc, "panel.min"),
			discordgo.TextInput{CustomID: "value", Label: T(c.loc, "panel.min_label"),
				Style: discordgo.TextInputShort, Value: strconv.Itoa(sv.MinPlayers), Required: true, MaxLength: 3})
	case "minset":
		v, err := strconv.Atoi(formValues(i.ModalSubmitData())["value"])
		if err != nil || v < 0 || v > maxMinPlayers {
			c.fail("err.bad_number", maxMinPlayers)
			return
		}
		if _, err := db.SetMinPlayers(c.guild(), sv.ID, v); err != nil {
			c.fail("err.internal")
			return
		}
		sv.MinPlayers = v
		c.update(panelView(c.loc, *sv, page))
	case "chan":
		if len(values) != 1 {
			return
		}
		if perms, err := s.State.UserChannelPermissions(s.State.User.ID, values[0]); err == nil && perms&invitePerms != invitePerms {
			c.fail("err.no_channel_perms", values[0])
			return
		}
		if _, err := db.SetChannel(c.guild(), sv.ID, values[0]); err != nil {
			c.fail("err.internal")
			return
		}
		sv.ChannelID, sv.Paused = values[0], false // picking a channel resumes paused posts
		c.update(panelView(c.loc, *sv, page))
	case "rm":
		c.update(confirmView(c.loc, *sv, page))
	case "rmyes":
		if _, err := db.RemoveServer(c.guild(), sv.ID); err != nil {
			c.fail("err.internal")
			return
		}
		c.update(listView(c.loc, c.guild(), page))
	}
}
