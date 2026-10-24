package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/rpfilomeno/discord-stats/internal/stats"
)

type GuildInfo struct {
	ID      int64
	Name    string
	Icon    string
	Members int64
}

func (s *Store) GuildInfo(id int64) (GuildInfo, error) {
	g := GuildInfo{ID: id}
	err := s.db.QueryRow(`SELECT name, icon, members FROM guilds WHERE id=?`, id).
		Scan(&g.Name, &g.Icon, &g.Members)
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil
	}
	return g, err
}

func (s *Store) LastHash(guildID int64) (string, error) {
	var h string
	err := s.db.QueryRow(`SELECT last_hash FROM guilds WHERE id=?`, guildID).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

func (s *Store) SetLastHash(guildID int64, hash string) error {
	_, err := s.db.Exec(`UPDATE guilds SET last_hash=? WHERE id=?`, hash, guildID)
	return err
}

// BuildStats recomputes the whole page from raw rows. At 10M messages this is a
// couple of seconds of index-only scans, far cheaper than incremental rollups.
func (s *Store) BuildStats(guildID int64, topN, chartDays int, now time.Time) (stats.Stats, error) {
	st := stats.Stats{GuildID: guildID, Generated: now}

	g, err := s.GuildInfo(guildID)
	if err != nil {
		return st, err
	}
	st.GuildName, st.Icon, st.Members = g.Name, g.Icon, int(g.Members)

	var since, until sql.NullInt64
	err = s.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT author_id),
		COALESCE(SUM(chars),0), COALESCE(SUM(has_link),0), COALESCE(SUM(attachments),0),
		COALESCE(SUM(is_reply),0), MIN(ts), MAX(ts)
		FROM messages WHERE guild_id=?`, guildID).
		Scan(&st.Total, &st.Active, &st.TotalChars, &st.PctLink, &st.PctFile, &st.PctReply, &since, &until)
	if err != nil {
		return st, err
	}
	if st.Total == 0 || !since.Valid || !until.Valid {
		return st, nil // empty guild: caller renders the "collecting" page
	}

	st.Since = time.Unix(since.Int64, 0).In(s.loc)
	st.Until = time.Unix(until.Int64, 0).In(s.loc)
	days := DayNumber(st.Until, s.loc) - DayNumber(st.Since, s.loc) + 1
	if days < 1 {
		days = 1
	}
	st.Days = int(days)
	st.AvgPerDay = float64(st.Total) / float64(days)
	st.AvgChars = float64(st.TotalChars) / float64(st.Total)

	// percentages from the raw sums, then reuse the fields as rates
	st.PctLink = 100 * st.PctLink / float64(st.Total)
	st.PctFile = 100 * st.PctFile / float64(st.Total)
	st.PctReply = 100 * st.PctReply / float64(st.Total)

	if err := s.daily(guildID, DayNumber(now, s.loc), chartDays, &st); err != nil {
		return st, err
	}
	if err := s.heat(guildID, &st); err != nil {
		return st, err
	}
	if err := s.channels(guildID, topN, &st); err != nil {
		return st, err
	}
	if err := s.members(guildID, topN, &st); err != nil {
		return st, err
	}
	if err := s.lists(guildID, &st); err != nil {
		return st, err
	}
	if err := s.coverage(&st); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) daily(guildID, today int64, chartDays int, st *stats.Stats) error {
	start := DayNumber(st.Since, s.loc)
	if v := today - int64(chartDays) + 1; v > start {
		start = v
	}
	counts := map[int64]int64{}
	rows, err := s.db.Query(`SELECT day, COUNT(*) FROM messages
		WHERE guild_id=? AND day>=? GROUP BY day`, guildID, start)
	if err != nil {
		return err
	}
	for rows.Next() {
		var d, n int64
		if err := rows.Scan(&d, &n); err != nil {
			rows.Close()
			return err
		}
		counts[d] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	up, err := s.uptime(start, today)
	if err != nil {
		return err
	}

	for d := start; d <= today; d++ {
		p := stats.Point{Day: d, Count: counts[d], Up: -1}
		if m, ok := up[d]; ok {
			p.Up = m
		}
		st.Daily = append(st.Daily, p)
	}
	return nil
}

func (s *Store) uptime(from, to int64) (map[int64]int, error) {
	out := map[int64]int{}
	rows, err := s.db.Query(`SELECT day, minutes FROM uptime WHERE day>=? AND day<=?`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d int64
		var m int
		if err := rows.Scan(&d, &m); err != nil {
			return nil, err
		}
		out[d] = m
	}
	return out, rows.Err()
}

func (s *Store) heat(guildID int64, st *stats.Stats) error {
	rows, err := s.db.Query(`SELECT (day+4)%7, hour, COUNT(*) FROM messages
		WHERE guild_id=? GROUP BY 1,2`, guildID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var dow, hour int
		var n int64
		if err := rows.Scan(&dow, &hour, &n); err != nil {
			return err
		}
		if dow < 0 {
			dow += 7
		}
		st.Heat[dow%7][hour%24] += n
	}
	return rows.Err()
}

func (s *Store) channels(guildID int64, topN int, st *stats.Stats) error {
	rows, err := s.db.Query(`SELECT COALESCE(NULLIF(c.name,''), '#'||m.channel_id), COUNT(*)
		FROM messages m LEFT JOIN channels c ON c.id=m.channel_id
		WHERE m.guild_id=? GROUP BY m.channel_id ORDER BY 2 DESC LIMIT ?`, guildID, topN)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var n stats.Named
		if err := rows.Scan(&n.Name, &n.Count); err != nil {
			return err
		}
		st.Channels = append(st.Channels, n)
	}
	return rows.Err()
}

func (s *Store) members(guildID int64, topN int, st *stats.Stats) error {
	rows, err := s.db.Query(`SELECT mb.alias, mb.name, mb.user_id, mb.avatar, COUNT(*), COALESCE(SUM(m.chars),0)
		FROM messages m JOIN members mb ON mb.guild_id=m.guild_id AND mb.user_id=m.author_id
		WHERE m.guild_id=? GROUP BY m.author_id ORDER BY 5 DESC LIMIT ?`, guildID, topN)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m stats.Member
		if err := rows.Scan(&m.Alias, &m.Name, &m.UserID, &m.Avatar, &m.Count, &m.Chars); err != nil {
			return err
		}
		m.Share = 100 * float64(m.Count) / float64(st.Total)
		st.MembersTop = append(st.MembersTop, m)
	}
	return rows.Err()
}

func (s *Store) lists(guildID int64, st *stats.Stats) error {
	var err error
	if st.Words, err = s.named(`SELECT word, n FROM word_counts WHERE guild_id=? ORDER BY n DESC LIMIT 120`, guildID); err != nil {
		return err
	}
	st.Emojis, err = s.named(`SELECT emoji, n FROM emoji_counts WHERE guild_id=? ORDER BY n DESC LIMIT 40`, guildID)
	return err
}

func (s *Store) named(q string, guildID int64) ([]stats.Named, error) {
	rows, err := s.db.Query(q, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []stats.Named
	for rows.Next() {
		var n stats.Named
		if err := rows.Scan(&n.Name, &n.Count); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) coverage(st *stats.Stats) error {
	first, err := s.firstUptimeDay()
	if err != nil {
		return err
	}
	if first == 0 {
		return nil
	}
	from := DayNumber(st.Since, s.loc)
	if first > from {
		from = first
	}
	to := DayNumber(st.Until, s.loc)
	if to < from {
		to = from
	}
	up, err := s.uptime(from, to)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, m := range up {
		total += int64(m)
	}
	window := (to - from + 1) * 1440
	if window > 0 {
		st.CoveragePct = int(100 * total / window)
	}
	return nil
}

func (s *Store) firstUptimeDay() (int64, error) {
	var d sql.NullInt64
	if err := s.db.QueryRow(`SELECT MIN(day) FROM uptime`).Scan(&d); err != nil {
		return 0, err
	}
	return d.Int64, nil
}

// PruneAll keeps the top `keep` words and emojis per guild so the tables cannot
// grow without bound. Two indexed queries per table; runs once per publish cycle.
func (s *Store) PruneAll(guildIDs []int64, keep int) error {
	for _, id := range guildIDs {
		if err := s.pruneTable("word_counts", "word", id, keep); err != nil {
			return err
		}
		if err := s.pruneTable("emoji_counts", "emoji", id, keep); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) pruneTable(table, col string, guildID int64, keep int) error {
	// "NOT IN" rather than a threshold: ties at the cutoff would otherwise keep
	// the whole tail (e.g. ten words with n=1 and keep=3).
	_, err := s.db.Exec(fmt.Sprintf(`DELETE FROM %[1]s WHERE guild_id=? AND %[2]s NOT IN (
		SELECT %[2]s FROM %[1]s WHERE guild_id=? ORDER BY n DESC, %[2]s LIMIT ?)`, table, col),
		guildID, guildID, keep)
	return err
}
