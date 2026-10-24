package stats

import "time"

type Point struct {
	Day   int64 // unix day in the configured timezone
	Count int64
	Up    int // uptime minutes for that day, -1 = unknown (before tracking started)
}

type Named struct {
	Name  string
	Count int64
}

type Member struct {
	Alias  int
	Name   string
	UserID int64
	Avatar string // CDN hash, "" means the default avatar
	Count  int64
	Chars  int64
	Share  float64
}

type Stats struct {
	GuildID   int64
	GuildName string
	Icon      string // Discord CDN url, public guild asset (never a user avatar)
	Members   int

	Generated time.Time
	Since     time.Time
	Until     time.Time

	Total       int64
	Active      int64
	Days        int
	AvgPerDay   float64
	TotalChars  int64
	AvgChars    float64
	PctLink     float64
	PctFile     float64
	PctReply    float64
	CoveragePct int // share of the period the collector was actually running

	Daily      []Point
	Heat       [7][24]int64
	Channels   []Named
	MembersTop []Member
	Words      []Named
	Emojis     []Named
}
