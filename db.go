package main

import (
	"database/sql"
	"strings"

	_ "github.com/ncruces/go-sqlite3/driver" // SQLite compiled to Wasm: pure Go, light to build
	_ "github.com/ncruces/go-sqlite3/embed"
)

type Store struct{ *sql.DB }

type Server struct {
	ID                          int64
	GuildID, ChannelID, Address string
	MinPlayers                  int
	LastMap, Name               string
	Style                       string // "full" or "compact"
	Paused                      bool   // posting failed for good (channel gone or no access)
}

// Label is how a server is shown in lists and autocomplete.
func (s Server) Label() string {
	if s.Name == "" {
		return s.Address
	}
	return s.Name + " (" + s.Address + ")"
}

type Notify struct {
	ID       int64
	UserID   string
	Pattern  string
	Server   Server // zero Server (ID 0) when the alert covers all servers in the guild
	Delivery string // the user's choice, "dm" or "channel", loaded with the alert
}

// AllServers reports an alert for every server tracked in its Discord server.
func (n Notify) AllServers() bool { return n.Server.ID == 0 }

const schema = `
CREATE TABLE IF NOT EXISTS servers (
	id INTEGER PRIMARY KEY,
	guild_id TEXT NOT NULL,
	channel_id TEXT NOT NULL,
	address TEXT NOT NULL,
	min_players INTEGER NOT NULL DEFAULT 0,
	last_map TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	UNIQUE(guild_id, address)
);
CREATE TABLE IF NOT EXISTS notifications (
	id INTEGER PRIMARY KEY,
	user_id TEXT NOT NULL,
	guild_id TEXT NOT NULL,
	server_id INTEGER REFERENCES servers(id) ON DELETE CASCADE, -- NULL = all servers in the guild
	pattern TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS user_prefs (
	user_id TEXT PRIMARY KEY,
	delivery TEXT NOT NULL DEFAULT 'dm'
);
CREATE TABLE IF NOT EXISTS guild_collections (
	guild_id TEXT NOT NULL,
	collection_id TEXT NOT NULL,
	PRIMARY KEY (guild_id, collection_id)
);
CREATE TABLE IF NOT EXISTS map_pictures (
	guild_id TEXT NOT NULL,
	map TEXT NOT NULL,
	url TEXT NOT NULL,
	PRIMARY KEY (guild_id, map)
);
-- When each game server's current map started, as first seen by Shiron. known = 0 when
-- Shiron never saw the change (the map was already running when tracking began).
CREATE TABLE IF NOT EXISTS map_starts (
	address TEXT PRIMARY KEY,
	map TEXT NOT NULL,
	since INTEGER NOT NULL,
	known INTEGER NOT NULL
);
-- Player history for /stat: one row per game server every 5 minutes, kept 30 days.
-- players is the real count (fake players removed); -1 means the server was offline.
CREATE TABLE IF NOT EXISTS player_samples (
	address TEXT NOT NULL,
	at INTEGER NOT NULL,
	players INTEGER NOT NULL,
	reported INTEGER NOT NULL,
	max INTEGER NOT NULL,
	map TEXT NOT NULL,
	PRIMARY KEY (address, at)
) WITHOUT ROWID;`

// Sample is one point of a server's player history.
type Sample struct {
	At                     int64
	Players, Reported, Max int // Players -1 = offline
	Map                    string
}

func (db *Store) AddSample(addr string, s Sample) error {
	_, err := db.Exec("INSERT OR REPLACE INTO player_samples (address, at, players, reported, max, map) VALUES (?,?,?,?,?,?)",
		addr, s.At, s.Players, s.Reported, s.Max, s.Map)
	return err
}

// Samples returns a server's history from a unix time on, oldest first.
func (db *Store) Samples(addr string, from int64) ([]Sample, error) {
	rows, err := db.Query("SELECT at, players, reported, max, map FROM player_samples WHERE address = ? AND at >= ? ORDER BY at", addr, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var s Sample
		if err := rows.Scan(&s.At, &s.Players, &s.Reported, &s.Max, &s.Map); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PruneSamples drops history older than before, and history of game servers that no
// Discord server tracks anymore.
func (db *Store) PruneSamples(before int64) error {
	_, err := db.Exec("DELETE FROM player_samples WHERE at < ? OR address NOT IN (SELECT address FROM servers)", before)
	return err
}

// MapStart returns what Shiron last recorded about a server's map; ok is false when
// it has no record.
func (db *Store) MapStart(addr string) (m mapStart, ok bool) {
	err := db.QueryRow("SELECT map, since, known FROM map_starts WHERE address = ?", addr).Scan(&m.name, &m.since, &m.known)
	return m, err == nil
}

func (db *Store) SetMapStart(addr string, m mapStart) error {
	_, err := db.Exec("INSERT INTO map_starts (address, map, since, known) VALUES (?,?,?,?) "+
		"ON CONFLICT(address) DO UPDATE SET map = excluded.map, since = excluded.since, known = excluded.known",
		addr, m.name, m.since, m.known)
	return err
}

func openDB(path string) (*Store, error) {
	d, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite allows one writer anyway, and it keeps ":memory:" a single DB in tests.
	// Because of this, always finish reading rows before running another query.
	d.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", schema} {
		if _, err := d.Exec(q); err != nil {
			d.Close()
			return nil, err
		}
	}
	// Columns added after the first release. Existing databases get them here; the
	// "duplicate column" error just means the database already has it.
	for _, q := range []string{
		"ALTER TABLE servers ADD COLUMN style TEXT NOT NULL DEFAULT 'full'",
		"ALTER TABLE servers ADD COLUMN paused INTEGER NOT NULL DEFAULT 0",
	} {
		if _, err := d.Exec(q); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			d.Close()
			return nil, err
		}
	}
	if err := migrateNotifications(d); err != nil {
		d.Close()
		return nil, err
	}
	for _, q := range []string{
		// IFNULL so two "all servers" alerts for the same map count as duplicates.
		"CREATE UNIQUE INDEX IF NOT EXISTS notifications_unique ON notifications(user_id, guild_id, IFNULL(server_id, 0), pattern)",
		"CREATE INDEX IF NOT EXISTS notifications_server ON notifications(server_id)",
		"CREATE INDEX IF NOT EXISTS notifications_guild ON notifications(guild_id)",
	} {
		if _, err := d.Exec(q); err != nil {
			d.Close()
			return nil, err
		}
	}
	return &Store{d}, nil
}

// migrateNotifications upgrades the alerts table from the first release (every alert
// tied to one server) to the current one (guild_id added, server_id NULL = all
// servers). SQLite can't relax NOT NULL in place, so the table is copied, keeping
// every alert and its ID.
func migrateNotifications(d *sql.DB) error {
	var hasGuild int
	if err := d.QueryRow("SELECT COUNT(*) FROM pragma_table_info('notifications') WHERE name = 'guild_id'").Scan(&hasGuild); err != nil || hasGuild > 0 {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE notifications_new (
			id INTEGER PRIMARY KEY,
			user_id TEXT NOT NULL,
			guild_id TEXT NOT NULL,
			server_id INTEGER REFERENCES servers(id) ON DELETE CASCADE,
			pattern TEXT NOT NULL
		)`,
		`INSERT INTO notifications_new (id, user_id, guild_id, server_id, pattern)
			SELECT n.id, n.user_id, s.guild_id, n.server_id, n.pattern FROM notifications n JOIN servers s ON s.id = n.server_id`,
		"DROP TABLE notifications",
		"ALTER TABLE notifications_new RENAME TO notifications",
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "UNIQUE") }

const serverCols = "id, guild_id, channel_id, address, min_players, last_map, name, style, paused"

func scanServers(rows *sql.Rows, err error) ([]Server, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.GuildID, &s.ChannelID, &s.Address, &s.MinPlayers, &s.LastMap, &s.Name, &s.Style, &s.Paused); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *Store) AddServer(s Server) (int64, error) {
	if s.Style == "" {
		s.Style = "full"
	}
	r, err := db.Exec(`INSERT INTO servers (guild_id, channel_id, address, min_players, last_map, name, style) VALUES (?,?,?,?,?,?,?)`,
		s.GuildID, s.ChannelID, s.Address, s.MinPlayers, s.LastMap, s.Name, s.Style)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// Servers lists one guild's servers, or every server when guild is "".
func (db *Store) Servers(guild string) ([]Server, error) {
	if guild == "" {
		return scanServers(db.Query("SELECT " + serverCols + " FROM servers ORDER BY id"))
	}
	return scanServers(db.Query("SELECT "+serverCols+" FROM servers WHERE guild_id = ? ORDER BY id", guild))
}

func (db *Store) Server(guild string, id int64) (*Server, error) {
	s, err := scanServers(db.Query("SELECT "+serverCols+" FROM servers WHERE guild_id = ? AND id = ?", guild, id))
	if err != nil {
		return nil, err
	}
	if len(s) == 0 {
		return nil, sql.ErrNoRows
	}
	return &s[0], nil
}

// ServerByID finds a server in any guild; used by card buttons, which also work in DMs.
func (db *Store) ServerByID(id int64) (*Server, error) {
	s, err := scanServers(db.Query("SELECT "+serverCols+" FROM servers WHERE id = ?", id))
	if err != nil {
		return nil, err
	}
	if len(s) == 0 {
		return nil, sql.ErrNoRows
	}
	return &s[0], nil
}

func affected(r sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n > 0, err
}

func (db *Store) RemoveServer(guild string, id int64) (bool, error) {
	return affected(db.Exec("DELETE FROM servers WHERE guild_id = ? AND id = ?", guild, id))
}

func (db *Store) SetMinPlayers(guild string, id int64, v int) (bool, error) {
	return affected(db.Exec("UPDATE servers SET min_players = ? WHERE guild_id = ? AND id = ?", v, guild, id))
}

// SetChannel moves a server's posts to another channel; picking a channel also
// resumes posts that were paused after failures.
func (db *Store) SetChannel(guild string, id int64, channelID string) (bool, error) {
	return affected(db.Exec("UPDATE servers SET channel_id = ?, paused = 0 WHERE guild_id = ? AND id = ?", channelID, guild, id))
}

// SetPaused stops (or resumes) posting for a server whose channel can't be used.
func (db *Store) SetPaused(id int64, paused bool) error {
	_, err := db.Exec("UPDATE servers SET paused = ? WHERE id = ?", paused, id)
	return err
}

func (db *Store) SetStyle(guild string, id int64, style string) (bool, error) {
	return affected(db.Exec("UPDATE servers SET style = ? WHERE guild_id = ? AND id = ?", style, guild, id))
}

func (db *Store) UpdateState(id int64, lastMap, name string) error {
	_, err := db.Exec("UPDATE servers SET last_map = ?, name = ? WHERE id = ?", lastMap, name, id)
	return err
}

func (db *Store) DeleteGuild(guild string) error {
	if _, err := db.Exec("DELETE FROM servers WHERE guild_id = ?", guild); err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM notifications WHERE guild_id = ?", guild); err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM map_pictures WHERE guild_id = ?", guild); err != nil {
		return err
	}
	if _, err := db.Exec("DELETE FROM guild_collections WHERE guild_id = ?", guild); err != nil {
		return err
	}
	// Users with no alerts left anywhere: drop their DM/channel choice too.
	_, err := db.Exec("DELETE FROM user_prefs WHERE user_id NOT IN (SELECT user_id FROM notifications)")
	return err
}

// Collections lists the Workshop collection IDs a guild linked for map pictures.
func (db *Store) Collections(guild string) []string {
	rows, err := db.Query("SELECT collection_id FROM guild_collections WHERE guild_id = ? ORDER BY rowid", guild)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// AddCollection links a collection to a guild (linking it twice is fine).
func (db *Store) AddCollection(guild, id string) error {
	_, err := db.Exec("INSERT OR IGNORE INTO guild_collections (guild_id, collection_id) VALUES (?, ?)", guild, id)
	return err
}

func (db *Store) ClearCollections(guild string) error {
	_, err := db.Exec("DELETE FROM guild_collections WHERE guild_id = ?", guild)
	return err
}

// MapPicture is an admin's picture override for a map in one guild, or "".
func (db *Store) MapPicture(guild, mapKey string) string {
	var u string
	db.QueryRow("SELECT url FROM map_pictures WHERE guild_id = ? AND map = ?", guild, mapKey).Scan(&u)
	return u
}

// SetMapPicture sets a map's picture override; an empty url removes it.
func (db *Store) SetMapPicture(guild, mapKey, url string) error {
	if url == "" {
		_, err := db.Exec("DELETE FROM map_pictures WHERE guild_id = ? AND map = ?", guild, mapKey)
		return err
	}
	_, err := db.Exec(`INSERT INTO map_pictures (guild_id, map, url) VALUES (?, ?, ?)
		ON CONFLICT(guild_id, map) DO UPDATE SET url = excluded.url`, guild, mapKey, url)
	return err
}

// AddNotify adds a map alert; serverID 0 means every server tracked in the guild.
func (db *Store) AddNotify(user, guild string, serverID int64, pattern string) error {
	var sid any // NULL for all servers
	if serverID != 0 {
		sid = serverID
	}
	_, err := db.Exec("INSERT INTO notifications (user_id, guild_id, server_id, pattern) VALUES (?,?,?,?)", user, guild, sid, pattern)
	return err
}

func (db *Store) RemoveNotify(user string, id int64) (bool, error) {
	return affected(db.Exec("DELETE FROM notifications WHERE user_id = ? AND id = ?", user, id))
}

// CountAlerts is how many map alerts exist across all guilds (for the status display).
func (db *Store) CountAlerts() int {
	var n int
	db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&n)
	return n
}

// RemoveAllNotifies deletes all of a user's alerts in one guild. If that leaves them
// no alerts anywhere, their saved DM/channel choice goes too, so nothing about the
// user is kept (Discord's Developer Policy). The choice is shared across guilds, so
// it stays while they still have alerts in another one.
func (db *Store) RemoveAllNotifies(user, guild string) error {
	if _, err := db.Exec("DELETE FROM notifications WHERE user_id = ? AND guild_id = ?", user, guild); err != nil {
		return err
	}
	_, err := db.Exec("DELETE FROM user_prefs WHERE user_id = ? AND NOT EXISTS (SELECT 1 FROM notifications WHERE user_id = ?)", user, user)
	return err
}

// EditNotify changes one of the user's alerts: its map pattern and where it applies
// (serverID 0 = all servers). A duplicate of another alert is a UNIQUE error.
func (db *Store) EditNotify(user string, id, serverID int64, pattern string) (bool, error) {
	var sid any
	if serverID != 0 {
		sid = serverID
	}
	return affected(db.Exec("UPDATE notifications SET server_id = ?, pattern = ? WHERE user_id = ? AND id = ?", sid, pattern, user, id))
}

// LEFT JOIN + COALESCE: all-server alerts have no server row and come back with Server.ID 0.
// The user's delivery choice comes along, so a map change needs one query, not one per user.
const notifySelect = `SELECT n.id, n.user_id, n.pattern, COALESCE(s.id, 0), n.guild_id, COALESCE(s.channel_id, ''),
	COALESCE(s.address, ''), COALESCE(s.min_players, 0), COALESCE(s.last_map, ''), COALESCE(s.name, ''), COALESCE(s.style, 'full'),
	COALESCE(s.paused, 0), COALESCE(p.delivery, 'dm')
	FROM notifications n LEFT JOIN servers s ON s.id = n.server_id LEFT JOIN user_prefs p ON p.user_id = n.user_id `

func scanNotifies(rows *sql.Rows, err error) ([]Notify, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notify
	for rows.Next() {
		var n Notify
		s := &n.Server
		if err := rows.Scan(&n.ID, &n.UserID, &n.Pattern, &s.ID, &s.GuildID, &s.ChannelID, &s.Address, &s.MinPlayers, &s.LastMap, &s.Name, &s.Style, &s.Paused, &n.Delivery); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Notifies lists a user's notifications in one guild.
func (db *Store) Notifies(user, guild string) ([]Notify, error) {
	return scanNotifies(db.Query(notifySelect+"WHERE n.user_id = ? AND n.guild_id = ? ORDER BY n.id", user, guild))
}

// NotifiesFor lists the alerts that apply to one tracked server: its own, plus the
// all-server alerts of its guild.
func (db *Store) NotifiesFor(sv Server) ([]Notify, error) {
	return scanNotifies(db.Query(notifySelect+"WHERE n.server_id = ? OR (n.server_id IS NULL AND n.guild_id = ?) ORDER BY n.id", sv.ID, sv.GuildID))
}

// Delivery is "dm" (default) or "channel".
func (db *Store) Delivery(user string) string {
	d := "dm"
	db.QueryRow("SELECT delivery FROM user_prefs WHERE user_id = ?", user).Scan(&d)
	return d
}

func (db *Store) SetDelivery(user, mode string) error {
	_, err := db.Exec(`INSERT INTO user_prefs (user_id, delivery) VALUES (?, ?)
		ON CONFLICT(user_id) DO UPDATE SET delivery = excluded.delivery`, user, mode)
	return err
}
