package db

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	if err := d.Ping(); err != nil {
		return nil, err
	}
	db := &DB{d}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

func (d *DB) migrate() error {
	_, err := d.Exec(`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL DEFAULT 'user',
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS libraries (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		path TEXT NOT NULL UNIQUE,
		drive_label TEXT,
		is_usb INTEGER DEFAULT 0,
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS media_items (
		id INTEGER PRIMARY KEY,
		library_id INTEGER REFERENCES libraries(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		original_title TEXT,
		year INTEGER,
		season INTEGER,
		episode INTEGER,
		path TEXT NOT NULL,
		ext TEXT,
		size INTEGER,
		duration INTEGER,
		width INTEGER,
		height INTEGER,
		codec TEXT,
		bitrate INTEGER,
		added_at TEXT NOT NULL,
		UNIQUE(library_id, path)
	);
	CREATE TABLE IF NOT EXISTS watch_progress (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		media_item_id INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
		position_seconds REAL NOT NULL DEFAULT 0,
		duration_seconds REAL,
		completed INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL,
		UNIQUE(user_id, media_item_id)
	);
	CREATE TABLE IF NOT EXISTS favorites (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		media_item_id INTEGER NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		UNIQUE(user_id, media_item_id)
	);
	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS iptv_lists (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		source_type TEXT NOT NULL DEFAULT 'url',
		source TEXT NOT NULL,
		updated_at TEXT NOT NULL DEFAULT ''
	);
	CREATE TABLE IF NOT EXISTS iptv_channels (
		id INTEGER PRIMARY KEY,
		list_id INTEGER NOT NULL REFERENCES iptv_lists(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		group_name TEXT NOT NULL DEFAULT '',
		logo TEXT NOT NULL DEFAULT '',
		url TEXT NOT NULL,
		UNIQUE(list_id, url, name)
	);
	CREATE INDEX IF NOT EXISTS idx_iptv_channels_list ON iptv_channels(list_id);
	CREATE INDEX IF NOT EXISTS idx_iptv_channels_group ON iptv_channels(list_id, group_name);`)
	if err != nil {
		return err
	}
	// Poster art: "file:<path>" for sidecar images, an https URL for online
	// matches, "-" when a lookup found nothing. Ignore the error when the
	// column already exists.
	d.Exec(`ALTER TABLE media_items ADD COLUMN poster TEXT NOT NULL DEFAULT ''`)
	// Synopsis and rating from the online lookup, shown in the details view.
	d.Exec(`ALTER TABLE media_items ADD COLUMN overview TEXT NOT NULL DEFAULT ''`)
	d.Exec(`ALTER TABLE media_items ADD COLUMN rating REAL NOT NULL DEFAULT 0`)
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }
