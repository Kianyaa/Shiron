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
	// Only commands that need typed input or open a panel; everything else is a button
	// in /servers list (admins) or /notify list (everyone).
	minPlayers := option(num, "min_players", "opt.min_players", false)
	minPlayers.MinValue, minPlayers.MaxValue = ptr(0.0), maxMinPlayers
	ip := option(str, "ip", "opt.address", true)
	ip.MaxLength = 100
	channel := option(discordgo.ApplicationCommandOptionChannel, "channel", "opt.channel", false)
	channel.ChannelTypes = []discordgo.ChannelType{discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews}
	mapName := option(str, "map", "opt.map", true)
	mapName.MaxLength = 64
	style := option(str, "style", "opt.style", false)
	style.Choices = []*discordgo.ApplicationCommandOptionChoice{
		{Name: "Full", Value: "full", NameLocalizations: localized("style.full")},
		{Name: "Compact", Value: "compact", NameLocalizations: localized("style.compact")},
	}

	servers := command("servers", "cmd.servers",
		option(sub, "add", "cmd.servers.add", false, ip, channel, minPlayers, style),
		option(sub, "list", "cmd.servers.list", false),
	)
	servers.DefaultMemberPermissions = ptr(int64(discordgo.PermissionManageGuild))

	anywhere := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild, discordgo.InteractionContextBotDM}
	ping, help, invite, about := command("ping", "cmd.ping"), command("help", "cmd.help"), command("invite", "cmd.invite"), command("about", "cmd.about")
	ping.Contexts, help.Contexts, invite.Contexts, about.Contexts = anywhere, anywhere, anywhere, anywhere

	return []*discordgo.ApplicationCommand{
		servers,
		command("map", "cmd.map", server(false)),
		command("players", "cmd.players", server(false)),
		command("notify", "cmd.notify",
			option(sub, "add", "cmd.notify.add", false, mapName, server(false)),
			option(sub, "list", "cmd.notify.list", false),
		),
		about, ping, help, invite,
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

func (c *ictx) edit(text string) {
	e := &discordgo.WebhookEdit{Content: &text, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if _, err := c.s.InteractionResponseEdit(c.i.Interaction, e); err != nil {
		slog.Warn("edit failed", "err", err)
	}
}

// server resolves the "server" option: an autocomplete ID, a typed address, or, when the
// option was left empty, the one server this channel (or this Discord server) tracks.
// Returns an i18n error key on failure.
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
		if sv := defaultServer(list, c.i.ChannelID); sv != nil {
			return sv, ""
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

// defaultServer picks the server meant when none was named: the only one tracked in
// this channel, else the only one in the Discord server. nil means the user must pick.
func defaultServer(list []Server, channelID string) *Server {
	var here []*Server
	for i := range list {
		if list[i].ChannelID == channelID {
			here = append(here, &list[i])
		}
	}
	switch {
	case len(here) == 1:
		return here[0]
	case len(list) == 1:
		return &list[0]
	}
	return nil
}

var handlers = map[string]func(*ictx){
	"ping": cmdPing, "help": cmdHelp, "invite": cmdInvite, "about": cmdAbout,
	"servers add": cmdServersAdd, "servers list": cmdServersList,
	"map": cmdMap, "players": cmdPlayers,
	"notify add": cmdNotifyAdd, "notify list": cmdNotifyList,
}

var anywhereCmds = map[string]bool{"ping": true, "help": true, "invite": true, "about": true}

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
		// Discord hides /servers from non-admins, but admins can grant it to other roles in
		// Server Settings > Integrations. Require Manage Server here too, so the command
		// and its Manage buttons agree on who may change things.
		if strings.HasPrefix(name, "servers ") && (i.Member == nil || i.Member.Permissions&discordgo.PermissionManageGuild == 0) {
			c.fail("err.not_admin")
			return
		}
		h(c)
	case discordgo.InteractionApplicationCommandAutocomplete:
		onAutocomplete(s, i)
	case discordgo.InteractionMessageComponent:
		switch id := i.MessageComponentData().CustomID; {
		case strings.HasPrefix(id, "srvm:"):
			onManage(s, i)
		case strings.HasPrefix(id, "ntf:"):
			onAlertList(s, i)
		case strings.HasPrefix(id, "about:"):
			onAbout(s, i)
		default:
			onButton(s, i)
		}
	case discordgo.InteractionModalSubmit:
		if strings.HasPrefix(i.ModalSubmitData().CustomID, "ntf:") {
			onAlertList(s, i)
		} else {
			onManage(s, i)
		}
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
	addr, err := resolveAddr(c.str("ip"))
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
		c.edit("❌ " + c.t("err.no_response", code(addr)))
		return
	}
	if !st.IsCS2() {
		c.edit("❌ " + c.t("err.not_cs2", code(addr)))
		return
	}
	sv := Server{GuildID: c.guild(), ChannelID: channelID, Address: addr, MinPlayers: minP, LastMap: st.Map, Name: st.Name, Style: cardStyle(c.str("style"))}
	_, err = db.AddServer(sv)
	switch {
	case isUnique(err):
		c.edit("❌ " + c.t("err.already_tracked", code(addr)))
	case err != nil:
		slog.Error("add server", "err", err)
		c.edit("❌ " + c.t("err.internal"))
	default:
		c.edit("✅ " + c.t("servers.added", sv.Display(), channelID, minP))
	}
}

// cardStyle turns the style option into a stored value; anything unexpected is "full".
func cardStyle(s string) string {
	if s == "compact" {
		return s
	}
	return "full"
}

// ---------- /map and /players ----------

// lookup is the shared start of /map and /players: pick the server, apply the
// cooldown, defer, and query it. Returns nil after replying if anything failed.
func (c *ictx) lookup() (*Server, *Status) {
	sv, errKey := c.server()
	if errKey != "" {
		c.fail(errKey)
		return nil, nil
	}
	if onCooldown(c.user()) {
		c.fail("err.cooldown", int(queryCooldown.Seconds()))
		return nil, nil
	}
	c.respondV2(false, skeletonCard(c.loc, *sv)) // instant answer instead of "is thinking…"
	st, err := queryInfo(sv.Address)
	if err != nil {
		c.editV2(textV2("❌ " + c.t("err.no_response", sv.Display())))
		return nil, nil
	}
	return sv, st
}

func cmdMap(c *ictx) {
	if sv, st := c.lookup(); sv != nil {
		c.editV2(serverCard(c.loc, *sv, st, ""))
	}
}

func cmdPlayers(c *ictx) {
	if sv, st := c.lookup(); sv != nil {
		c.editV2(withText(fullCard(c.loc, *sv, st, ""), playerList(c.loc, sv.Address, st.Humans()))) // a scoreboard needs the full card
	}
}

// ---------- /notify ----------

// matchMap reports whether a map name matches a notify pattern ("de_dust2", "de_*").
func matchMap(pattern, mapName string) bool {
	ok, _ := path.Match(pattern, strings.ToLower(mapName))
	return ok
}

func cmdNotifyAdd(c *ictx) {
	pattern, err := cleanPattern(c.str("map"))
	if err != nil {
		c.fail("err.bad_pattern")
		return
	}
	// No server chosen means every server tracked here (now and later), so a map is
	// caught wherever it comes up.
	var serverID int64
	where := c.t("notify.all_servers")
	if c.str("server") != "" {
		sv, errKey := c.server()
		if errKey != "" {
			c.fail(errKey)
			return
		}
		serverID, where = sv.ID, sv.Display()
	} else if list, err := db.Servers(c.guild()); err != nil || len(list) == 0 {
		c.fail("err.no_servers")
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
	switch err := db.AddNotify(c.user(), c.guild(), serverID, pattern); {
	case isUnique(err):
		c.fail("err.already_notify", code(pattern))
	case err != nil:
		c.fail("err.internal")
	default:
		c.ok("notify.added", code(pattern), where, c.t("mode."+db.Delivery(c.user())))
	}
}

// notifyWhere names where an alert applies, safe for a message.
func notifyWhere(loc string, n Notify) string {
	if n.AllServers() {
		return T(loc, "notify.all_servers")
	}
	return n.Server.Display()
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
