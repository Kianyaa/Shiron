package main

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct{ *sql.DB }

type Server struct {
	ID                          int64
	GuildID, ChannelID, Address string
	MinPlayers                  int
	LastMap, Name               string
}

// Label is how a server is shown in lists and autocomplete.
func (s Server) Label() string {
	if s.Name == "" {
		return s.Address
	}
	return s.Name + " (" + s.Address + ")"
}

type Notify struct {
	ID      int64
	UserID  string
	Pattern string
	Server  Server
}

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
	server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
	pattern TEXT NOT NULL,
	UNIQUE(user_id, server_id, pattern)
);
CREATE INDEX IF NOT EXISTS notifications_server ON notifications(server_id);
CREATE TABLE IF NOT EXISTS user_prefs (
	user_id TEXT PRIMARY KEY,
	delivery TEXT NOT NULL DEFAULT 'dm'
);`

func openDB(path string) (*Store, error) {
	d, err := sql.Open("sqlite", path)
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
	return &Store{d}, nil
}

func isUnique(err error) bool { return err != nil && strings.Contains(err.Error(), "UNIQUE") }

const serverCols = "id, guild_id, channel_id, address, min_players, last_map, name"

func scanServers(rows *sql.Rows, err error) ([]Server, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.GuildID, &s.ChannelID, &s.Address, &s.MinPlayers, &s.LastMap, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (db *Store) AddServer(s Server) (int64, error) {
	r, err := db.Exec(`INSERT INTO servers (guild_id, channel_id, address, min_players, last_map, name) VALUES (?,?,?,?,?,?)`,
		s.GuildID, s.ChannelID, s.Address, s.MinPlayers, s.LastMap, s.Name)
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

func (db *Store) UpdateState(id int64, lastMap, name string) error {
	_, err := db.Exec("UPDATE servers SET last_map = ?, name = ? WHERE id = ?", lastMap, name, id)
	return err
}

func (db *Store) DeleteGuild(guild string) error {
	_, err := db.Exec("DELETE FROM servers WHERE guild_id = ?", guild)
	return err
}

func (db *Store) AddNotify(user string, serverID int64, pattern string) error {
	_, err := db.Exec("INSERT INTO notifications (user_id, server_id, pattern) VALUES (?,?,?)", user, serverID, pattern)
	return err
}

func (db *Store) RemoveNotify(user string, id int64) (bool, error) {
	return affected(db.Exec("DELETE FROM notifications WHERE user_id = ? AND id = ?", user, id))
}

const notifySelect = `SELECT n.id, n.user_id, n.pattern, s.id, s.guild_id, s.channel_id, s.address, s.min_players, s.last_map, s.name
	FROM notifications n JOIN servers s ON s.id = n.server_id `

func scanNotifies(rows *sql.Rows, err error) ([]Notify, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notify
	for rows.Next() {
		var n Notify
		s := &n.Server
		if err := rows.Scan(&n.ID, &n.UserID, &n.Pattern, &s.ID, &s.GuildID, &s.ChannelID, &s.Address, &s.MinPlayers, &s.LastMap, &s.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Notifies lists a user's notifications in one guild.
func (db *Store) Notifies(user, guild string) ([]Notify, error) {
	return scanNotifies(db.Query(notifySelect+"WHERE n.user_id = ? AND s.guild_id = ? ORDER BY n.id", user, guild))
}

// NotifiesFor lists every notification on one tracked server.
func (db *Store) NotifiesFor(serverID int64) ([]Notify, error) {
	return scanNotifies(db.Query(notifySelect+"WHERE n.server_id = ? ORDER BY n.id", serverID))
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
