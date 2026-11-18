package main

import (
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

const (
	maxServersPerGuild = 25 // also Discord's autocomplete limit
	maxNotifiesPerUser = 25
	maxMinPlayers      = 128
	colorShiron        = 0x5865F2
	invitePerms        = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks
)

func ptr[T any](v T) *T { return &v }

// ---------- command definitions ----------

// localized builds the Thai/Chinese translations of a description key.
func localized(key string) map[discordgo.Locale]string {
	return map[discordgo.Locale]string{discordgo.Thai: T("th", key), discordgo.ChineseCN: T("zh-CN", key)}
}

func option(t discordgo.ApplicationCommandOptionType, name, key string, required bool, sub ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: t, Name: name, Description: T("en", key),
		DescriptionLocalizations: localized(key), Required: required, Options: sub}
}

func command(name, key string, opts ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{Name: name, Description: T("en", key), DescriptionLocalizations: ptr(localized(key)),
		Options: opts, Contexts: &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}}
}

func commandDefs() []*discordgo.ApplicationCommand {
	const (
		sub = discordgo.ApplicationCommandOptionSubCommand
		str = discordgo.ApplicationCommandOptionString
		num = discordgo.ApplicationCommandOptionInteger
	)
	server := func(required bool) *discordgo.ApplicationCommandOption {
		o := option(str, "server", "opt.server", required)
		o.Autocomplete = true
		return o
	}
	minPlayers := func(required bool) *discordgo.ApplicationCommandOption {
		o := option(num, "min_players", "opt.min_players", required)
		o.MinValue, o.MaxValue = ptr(0.0), maxMinPlayers
		return o
	}
	address := option(str, "address", "opt.address", true)
	address.MaxLength = 100
	channel := option(discordgo.ApplicationCommandOptionChannel, "channel", "opt.channel", false)
	channel.ChannelTypes = []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}
	mapName := option(str, "map", "opt.map", true)
	mapName.MaxLength = 64
	notification := option(str, "notification", "opt.notification", true)
	notification.Autocomplete = true
	mode := option(str, "mode", "opt.mode", true)
	mode.Choices = []*discordgo.ApplicationCommandOptionChoice{
		{Name: "DM", Value: "dm", NameLocalizations: localized("choice.dm")},
		{Name: "Channel", Value: "channel", NameLocalizations: localized("choice.channel")},
	}

	servers := command("servers", "cmd.servers",
		option(sub, "add", "cmd.servers.add", false, address, channel, minPlayers(false)),
		option(sub, "list", "cmd.servers.list", false),
		option(sub, "remove", "cmd.servers.remove", false, server(true)),
		option(sub, "min-players", "cmd.servers.min_players", false, server(true), minPlayers(true)),
	)
	servers.DefaultMemberPermissions = ptr(int64(discordgo.PermissionManageGuild))

	anywhere := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM}
	ping, help, invite := command("ping", "cmd.ping"), command("help", "cmd.help"), command("invite", "cmd.invite")
	ping.Contexts, help.Contexts, invite.Contexts = anywhere, anywhere, anywhere

	return []*discordgo.ApplicationCommand{
		servers,
		command("map", "cmd.map", server(false)),
		command("players", "cmd.players", server(false)),
		command("notify", "cmd.notify",
			option(sub, "add", "cmd.notify.add", false, mapName, server(false)),
			option(sub, "remove", "cmd.notify.remove", false, notification),
			option(sub, "list", "cmd.notify.list", false),
			option(sub, "mode", "cmd.notify.mode", false, mode),
		),
		ping, help, invite,
	}
}

// ---------- interaction plumbing ----------

type ictx struct {
	s    *discordgo.Session
	i    *discordgo.InteractionCreate
	loc  string
	opts map[string]*discordgo.ApplicationCommandInteractionDataOption
}

func newCtx(s *discordgo.Session, i *discordgo.InteractionCreate) (*ictx, string) {
	c := &ictx{s: s, i: i, loc: string(i.Locale), opts: map[string]*discordgo.ApplicationCommandInteractionDataOption{}}
	data := i.ApplicationCommandData()
	name, opts := data.Name, data.Options
	if len(opts) == 1 && opts[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		name, opts = name+" "+opts[0].Name, opts[0].Options
	}
	for _, o := range opts {
		c.opts[o.Name] = o
	}
	return c, name
}

func (c *ictx) t(key string, args ...any) string { return T(c.loc, key, args...) }
func (c *ictx) guild() string                    { return c.i.GuildID }

func (c *ictx) user() string {
	if c.i.Member != nil {
		return c.i.Member.User.ID
	}
	return c.i.User.ID
}

func (c *ictx) str(name string) string {
	if o, ok := c.opts[name]; ok {
		return strings.TrimSpace(o.StringValue())
	}
	return ""
}

func (c *ictx) int(name string, def int) int {
	if o, ok := c.opts[name]; ok {
		return int(o.IntValue())
	}
	return def
}

func (c *ictx) respond(data *discordgo.InteractionResponseData, ephemeral bool) {
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	data.AllowedMentions = &discordgo.MessageAllowedMentions{} // replies never ping anyone
	err := c.s.InteractionRespond(c.i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data})
	if err != nil {
		slog.Warn("respond failed", "err", err)
	}
}

func (c *ictx) reply(text string)            { c.respond(&discordgo.InteractionResponseData{Content: text}, true) }
func (c *ictx) fail(key string, args ...any) { c.reply("❌ " + c.t(key, args...)) }
func (c *ictx) ok(key string, args ...any)   { c.reply("✅ " + c.t(key, args...)) }

// deferReply is for commands that query a CS2 server: Discord needs an answer within 3s.
func (c *ictx) deferReply(ephemeral bool) {
	data := &discordgo.InteractionResponseData{}
	if ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	if err := c.s.InteractionRespond(c.i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource, Data: data}); err != nil {
		slog.Warn("defer failed", "err", err)
	}
}

func (c *ictx) edit(text string, embed *discordgo.MessageEmbed, comps ...discordgo.MessageComponent) {
	e := &discordgo.WebhookEdit{Content: &text, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if embed != nil {
		e.Embeds = &[]*discordgo.MessageEmbed{embed}
	}
	if len(comps) > 0 {
		e.Components = &comps
	}
	if _, err := c.s.InteractionResponseEdit(c.i.Interaction, e); err != nil {
		slog.Warn("edit failed", "err", err)
	}
}

// server resolves the "server" option: an autocomplete ID, a typed address, or the
// only tracked server when the option was left empty. Returns an i18n error key on failure.
func (c *ictx) server() (*Server, string) {
	list, err := db.Servers(c.guild())
	if err != nil {
		slog.Error("list servers", "err", err)
		return nil, "err.internal"
	}
	if len(list) == 0 {
		return nil, "err.no_servers"
	}
	raw := c.str("server")
	if raw == "" {
		if len(list) == 1 {
			return &list[0], ""
		}
		return nil, "err.pick_server"
	}
	for i := range list {
		if strconv.FormatInt(list[i].ID, 10) == raw || strings.EqualFold(list[i].Address, raw) {
			return &list[i], ""
		}
	}
	return nil, "err.server_not_found"
}

var handlers = map[string]func(*ictx){
	"ping": cmdPing, "help": cmdHelp, "invite": cmdInvite,
	"servers add": cmdServersAdd, "servers list": cmdServersList, "servers remove": cmdServersRemove,
	"servers min-players": cmdServersMinPlayers,
	"map":                 cmdMap, "players": cmdPlayers,
	"notify add": cmdNotifyAdd, "notify remove": cmdNotifyRemove, "notify list": cmdNotifyList, "notify mode": cmdNotifyMode,
}

var anywhereCmds = map[string]bool{"ping": true, "help": true, "invite": true}

func onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	defer guard("interaction")
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		c, name := newCtx(s, i)
		h, ok := handlers[name]
		if !ok {
			return
		}
		if c.guild() == "" && !anywhereCmds[name] {
			c.fail("err.guild_only")
			return
		}
		h(c)
	case discordgo.InteractionApplicationCommandAutocomplete:
		onAutocomplete(s, i)
	}
}

// ---------- general ----------

func cmdPing(c *ictx) { c.reply(c.t("ping.reply", c.s.HeartbeatLatency().Milliseconds())) }
func cmdHelp(c *ictx) { c.reply(c.t("help.text")) }

func inviteURL(appID string) string {
	return fmt.Sprintf("https://discord.com/oauth2/authorize?client_id=%s&permissions=%d&scope=bot+applications.commands", appID, invitePerms)
}

func cmdInvite(c *ictx) { c.reply(c.t("invite.reply", inviteURL(c.s.State.User.ID))) }

// ---------- /servers ----------

func cmdServersAdd(c *ictx) {
	list, err := db.Servers(c.guild())
	if err != nil {
		c.fail("err.internal")
		return
	}
	if len(list) >= maxServersPerGuild {
		c.fail("err.too_many_servers", maxServersPerGuild)
		return
	}
	addr, err := resolveAddr(c.str("address"))
	if err != nil {
		c.fail("err.bad_address")
		return
	}
	channelID := c.i.ChannelID
	if o, ok := c.opts["channel"]; ok {
		channelID, _ = o.Value.(string)
	}
	if perms, err := c.s.State.UserChannelPermissions(c.s.State.User.ID, channelID); err == nil && perms&invitePerms != invitePerms {
		c.fail("err.no_channel_perms", channelID)
		return
	}
	minP := c.int("min_players", 0)

	c.deferReply(true)
	st, err := queryInfo(addr)
	if err != nil {
		c.edit("❌ "+c.t("err.no_response", code(addr)), nil)
		return
	}
	sv := Server{GuildID: c.guild(), ChannelID: channelID, Address: addr, MinPlayers: minP, LastMap: st.Map, Name: st.Name}
	_, err = db.AddServer(sv)
	switch {
	case isUnique(err):
		c.edit("❌ "+c.t("err.already_tracked", code(addr)), nil)
	case err != nil:
		slog.Error("add server", "err", err)
		c.edit("❌ "+c.t("err.internal"), nil)
	default:
		c.edit("✅ "+c.t("servers.added", sv.Display(), channelID, minP), nil)
	}
}

func cmdServersList(c *ictx) {
	list, err := db.Servers(c.guild())
	if err != nil {
		c.fail("err.internal")
		return
	}
	if len(list) == 0 {
		c.reply(c.t("err.no_servers"))
		return
	}
	var b strings.Builder
	for _, s := range list {
		fmt.Fprintf(&b, "%s\n%s\n\n", s.Display(), c.t("servers.list_line", s.ChannelID, s.MinPlayers, code(s.LastMap)))
	}
	c.respond(&discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{{
		Title: c.t("servers.list_title"), Description: truncate(b.String(), 4000), Color: colorShiron}}}, true)
}

func cmdServersRemove(c *ictx) {
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return
	}
	if _, err := db.RemoveServer(c.guild(), sv.ID); err != nil {
		c.fail("err.internal")
		return
	}
	c.ok("servers.removed", sv.Display())
}

func cmdServersMinPlayers(c *ictx) {
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return
	}
	v := c.int("min_players", 0)
	if _, err := db.SetMinPlayers(c.guild(), sv.ID, v); err != nil {
		c.fail("err.internal")
		return
	}
	c.ok("servers.min_set", sv.Display(), v)
}

// ---------- /map and /players ----------

// statusEmbed shows a server's live state. The server name goes in the title, which
// Discord shows as plain text, so a name can't smuggle in links or formatting.
func statusEmbed(loc, addr string, st *Status) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: truncate(st.Name, 256), Color: colorShiron, Fields: []*discordgo.MessageEmbedField{
		{Name: T(loc, "field.map"), Value: code(st.Map), Inline: true},
		{Name: T(loc, "field.players"), Value: T(loc, "players.count", st.Humans(), st.Max, st.Bots), Inline: true},
		{Name: T(loc, "field.connect"), Value: code("connect " + addr)},
	}}
	if img := mapImage(st.Map); img != "" {
		e.Image = &discordgo.MessageEmbedImage{URL: img}
	}
	return e
}

// joinButton is a link button that opens CS2 and joins the server, via the
// https page in SHIRON_CONNECT_URL (Discord only allows http(s) links on buttons).
func joinButton(loc, addr string) []discordgo.MessageComponent {
	if cfg.ConnectURL == "" {
		return nil
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: T(loc, "button.join"), Style: discordgo.LinkButton, URL: cfg.ConnectURL + "#" + addr},
	}}}
}

func cmdMap(c *ictx) {
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return
	}
	if onCooldown(c.user()) {
		c.fail("err.cooldown", int(queryCooldown.Seconds()))
		return
	}
	c.deferReply(false)
	st, err := queryInfo(sv.Address)
	if err != nil {
		c.edit("❌ "+c.t("err.no_response", sv.Display()), nil)
		return
	}
	c.edit("", statusEmbed(c.loc, sv.Address, st), joinButton(c.loc, sv.Address)...)
}

func cmdPlayers(c *ictx) {
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return
	}
	if onCooldown(c.user()) {
		c.fail("err.cooldown", int(queryCooldown.Seconds()))
		return
	}
	c.deferReply(false)
	st, err := queryInfo(sv.Address)
	if err != nil {
		c.edit("❌ "+c.t("err.no_response", sv.Display()), nil)
		return
	}
	e := statusEmbed(c.loc, sv.Address, st)
	players, err := queryPlayers(sv.Address)
	// A code block: player names are chosen by players, so nothing in them may render.
	var b strings.Builder
	for _, p := range players {
		if p.Name == "" {
			continue // connecting players have no name yet
		}
		name := truncate(strings.ReplaceAll(p.Name, "`", "'"), 24)
		fmt.Fprintf(&b, "%-24s %5d  %s\n", name, p.Score, (time.Duration(p.Duration) * time.Second).Truncate(time.Minute))
	}
	switch {
	case err == nil && b.Len() > 0:
		e.Description = "```\n" + truncate(b.String(), 3900) + "```"
	case st.Humans() > 0:
		e.Description = c.t("players.hidden")
	default:
		e.Description = c.t("players.empty")
	}
	c.edit("", e, joinButton(c.loc, sv.Address)...)
}

// ---------- /notify ----------

// matchMap reports whether a map name matches a notify pattern ("de_dust2", "de_*").
func matchMap(pattern, mapName string) bool {
	ok, _ := path.Match(pattern, strings.ToLower(mapName))
	return ok
}

func cmdNotifyAdd(c *ictx) {
	pattern := strings.ToLower(c.str("map"))
	if _, err := path.Match(pattern, ""); err != nil || pattern == "" || len(pattern) > 64 {
		c.fail("err.bad_pattern")
		return
	}
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return
	}
	existing, err := db.Notifies(c.user(), c.guild())
	if err != nil {
		c.fail("err.internal")
		return
	}
	if len(existing) >= maxNotifiesPerUser {
		c.fail("err.too_many_notifies", maxNotifiesPerUser)
		return
	}
	switch err := db.AddNotify(c.user(), sv.ID, pattern); {
	case isUnique(err):
		c.fail("err.already_notify", code(pattern))
	case err != nil:
		c.fail("err.internal")
	default:
		c.ok("notify.added", code(pattern), sv.Display(), c.t("mode."+db.Delivery(c.user())))
	}
}

func cmdNotifyRemove(c *ictx) {
	id, _ := strconv.ParseInt(c.str("notification"), 10, 64)
	removed, err := db.RemoveNotify(c.user(), id)
	switch {
	case err != nil:
		c.fail("err.internal")
	case !removed:
		c.fail("err.notify_not_found")
	default:
		c.ok("notify.removed")
	}
}

func cmdNotifyList(c *ictx) {
	list, err := db.Notifies(c.user(), c.guild())
	if err != nil {
		c.fail("err.internal")
		return
	}
	mode := c.t("mode." + db.Delivery(c.user()))
	if len(list) == 0 {
		c.reply(c.t("notify.none", mode))
		return
	}
	var b strings.Builder
	for _, n := range list {
		fmt.Fprintf(&b, "%s — %s\n", code(n.Pattern), n.Server.Display())
	}
	c.reply(truncate(c.t("notify.list", mode)+"\n"+b.String(), 2000))
}

func cmdNotifyMode(c *ictx) {
	mode := c.str("mode")
	if mode != "dm" && mode != "channel" {
		c.fail("err.internal")
		return
	}
	if err := db.SetDelivery(c.user(), mode); err != nil {
		c.fail("err.internal")
		return
	}
	c.ok("notify.mode_set", c.t("mode."+mode))
}

// ---------- autocomplete ----------

func onAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c, _ := newCtx(s, i)
	var focused *discordgo.ApplicationCommandInteractionDataOption
	for _, o := range c.opts {
		if o.Focused {
			focused = o
		}
	}
	if focused == nil || c.guild() == "" {
		return
	}
	typed := strings.ToLower(focused.StringValue())
	var choices []*discordgo.ApplicationCommandOptionChoice
	add := func(label string, id int64) {
		if len(choices) < 25 && strings.Contains(strings.ToLower(label), typed) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: truncate(label, 100), Value: strconv.FormatInt(id, 10)})
		}
	}
	switch focused.Name {
	case "server":
		list, _ := db.Servers(c.guild())
		for _, sv := range list {
			add(sv.Label(), sv.ID)
		}
	case "notification":
		list, _ := db.Notifies(c.user(), c.guild())
		for _, n := range list {
			add(n.Pattern+" — "+n.Server.Label(), n.ID)
		}
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices}})
	if err != nil {
		slog.Warn("autocomplete failed", "err", err)
	}
}

// ---------- helpers ----------

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// code shows untrusted text (server names, map names) as inline code, where Discord
// renders no links, mentions or formatting. Backticks and newlines are removed so it
// can't break out.
func code(s string) string {
	s = strings.NewReplacer("`", "'", "\n", " ", "\r", " ").Replace(strings.TrimSpace(s))
	if s == "" {
		s = "-"
	}
	return "`" + truncate(s, 100) + "`"
}

// Display is a server label that is safe to put in a message.
func (s Server) Display() string { return code(s.Label()) }

const queryCooldown = 5 * time.Second

var (
	cooldownMu sync.Mutex
	lastQuery  = map[string]time.Time{}
)

// onCooldown limits /map and /players to one per user every few seconds, so nobody
// can use Shiron to flood a game server with queries.
func onCooldown(user string) bool {
	cooldownMu.Lock()
	defer cooldownMu.Unlock()
	if time.Since(lastQuery[user]) < queryCooldown {
		return true
	}
	if len(lastQuery) > 10000 {
		clear(lastQuery) // keep memory bounded; losing old timestamps is harmless
	}
	lastQuery[user] = time.Now()
	return false
}
