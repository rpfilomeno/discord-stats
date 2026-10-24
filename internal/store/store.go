package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Message struct {
	ID        int64
	GuildID   int64
	ChannelID int64

	AuthorID     int64
	AuthorName   string
	AuthorAvatar string
	TS           int64
	Chars        int64
	Attachments  int64
	HasLink      bool
	IsReply      bool
	Words        []string
	Emojis       []string
}

type Store struct {
	db  *sql.DB
	loc *time.Location
}

const schema = `
CREATE TABLE IF NOT EXISTS guilds(
  id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT '', icon TEXT NOT NULL DEFAULT '',
  members INTEGER NOT NULL DEFAULT 0, last_hash TEXT NOT NULL DEFAULT '');

CREATE TABLE IF NOT EXISTS channels(
  id INTEGER PRIMARY KEY, guild_id INTEGER NOT NULL, name TEXT NOT NULL DEFAULT '',
  type INTEGER NOT NULL DEFAULT 0);

CREATE TABLE IF NOT EXISTS members(
  guild_id INTEGER NOT NULL, user_id INTEGER NOT NULL, alias INTEGER NOT NULL,
  name TEXT NOT NULL DEFAULT '', avatar TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(guild_id, user_id)) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS messages(
  id INTEGER PRIMARY KEY, guild_id INTEGER NOT NULL, channel_id INTEGER NOT NULL,
  author_id INTEGER NOT NULL, ts INTEGER NOT NULL, day INTEGER NOT NULL, hour INTEGER NOT NULL,
  chars INTEGER NOT NULL, attachments INTEGER NOT NULL,
  has_link INTEGER NOT NULL, is_reply INTEGER NOT NULL);

CREATE INDEX IF NOT EXISTS messages_guild_day ON messages(guild_id, day);
CREATE INDEX IF NOT EXISTS messages_guild_author ON messages(guild_id, author_id);

CREATE TABLE IF NOT EXISTS word_counts(
  guild_id INTEGER NOT NULL, word TEXT NOT NULL, n INTEGER NOT NULL,
  PRIMARY KEY(guild_id, word)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS word_counts_guild_n ON word_counts(guild_id, n DESC);

CREATE TABLE IF NOT EXISTS emoji_counts(
  guild_id INTEGER NOT NULL, emoji TEXT NOT NULL, n INTEGER NOT NULL,
  PRIMARY KEY(guild_id, emoji)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS emoji_counts_guild_n ON emoji_counts(guild_id, n DESC);

CREATE TABLE IF NOT EXISTS uptime(day INTEGER PRIMARY KEY, minutes INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

func Open(dir string, loc *time.Location) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dir, "stats.db") +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: a single writer goroutine, reads only at publish time.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db, loc: loc}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DayNumber is the local calendar day as days since the unix epoch. DST-safe.
func DayNumber(t time.Time, loc *time.Location) int64 {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}

func (s *Store) EnsureGuild(id int64, name, icon string, members int64) error {
	_, err := s.db.Exec(`INSERT INTO guilds(id,name,icon,members) VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, icon=excluded.icon, members=excluded.members`,
		id, name, icon, members)
	return err
}

func (s *Store) EnsureChannel(id, guildID int64, name string, typ int64) error {
	_, err := s.db.Exec(`INSERT INTO channels(id,guild_id,name,type) VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type`,
		id, guildID, name, typ)
	return err
}

// InsertBatch writes one batch in a single transaction and is idempotent:
// a re-delivered message id is skipped and does not double-count words.
func (s *Store) InsertBatch(msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insMsg, err := tx.Prepare(`INSERT INTO messages
		(id,guild_id,channel_id,author_id,ts,day,hour,chars,attachments,has_link,is_reply)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`)
	if err != nil {
		return err
	}
	defer insMsg.Close()

	insMember, err := tx.Prepare(`INSERT INTO members(guild_id,user_id,alias,name,avatar)
		VALUES(?,?,(SELECT COALESCE(MAX(alias),0)+1 FROM members WHERE guild_id=?),?,?)
		ON CONFLICT(guild_id,user_id) DO UPDATE SET
			name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE name END,
			avatar = CASE WHEN excluded.avatar <> '' THEN excluded.avatar ELSE avatar END`)
	if err != nil {
		return err
	}
	defer insMember.Close()

	insWord, err := tx.Prepare(`INSERT INTO word_counts(guild_id,word,n) VALUES(?,?,1)
		ON CONFLICT(guild_id,word) DO UPDATE SET n=n+1`)
	if err != nil {
		return err
	}
	defer insWord.Close()

	insEmoji, err := tx.Prepare(`INSERT INTO emoji_counts(guild_id,emoji,n) VALUES(?,?,1)
		ON CONFLICT(guild_id,emoji) DO UPDATE SET n=n+1`)
	if err != nil {
		return err
	}
	defer insEmoji.Close()

	for _, m := range msgs {
		t := time.Unix(m.TS, 0)
		res, err := insMsg.Exec(m.ID, m.GuildID, m.ChannelID, m.AuthorID, m.TS,
			DayNumber(t, s.loc), int64(t.In(s.loc).Hour()),
			m.Chars, m.Attachments, boolInt(m.HasLink), boolInt(m.IsReply))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // already counted
		}
		if _, err := insMember.Exec(m.GuildID, m.AuthorID, m.GuildID, m.AuthorName, m.AuthorAvatar); err != nil {
			return err
		}
		for _, w := range m.Words {
			if _, err := insWord.Exec(m.GuildID, w); err != nil {
				return err
			}
		}
		for _, e := range m.Emojis {
			if _, err := insEmoji.Exec(m.GuildID, e); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (s *Store) TickUptime(day int64) error {
	_, err := s.db.Exec(`INSERT INTO uptime(day,minutes) VALUES(?,1)
		ON CONFLICT(day) DO UPDATE SET minutes=minutes+1`, day)
	return err
}

func (s *Store) KV(k string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetKV(k, v string) error {
	_, err := s.db.Exec(`INSERT INTO kv(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}
