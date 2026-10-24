package render

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/rpfilomeno/discord-stats/internal/stats"
)

func sample() stats.Stats {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	day := now.Unix() / 86400
	s := stats.Stats{
		GuildID: 1124533898764890132, GuildName: "Test <Guild>", Icon: "abc",
		Members: 42, Generated: now, Since: now.AddDate(0, 0, -30), Until: now,
		Total: 12345, Active: 7, Days: 30, AvgPerDay: 411.5, AvgChars: 33.2,
		PctLink: 4.2, PctFile: 1.1, PctReply: 12.5, CoveragePct: 97,
		Daily:    []stats.Point{{Day: day - 1, Count: 100, Up: 1440}, {Day: day, Count: 200, Up: 1440}},
		Channels: []stats.Named{{Name: "general", Count: 100}, {Name: "off-topic", Count: 50}},
		MembersTop: []stats.Member{
			{Alias: 1, Name: "alice***", UserID: 123456789012345678, Avatar: "abc123", Count: 100, Chars: 3000, Share: 40},
			{Alias: 2, Name: "bob***", UserID: 223456789012345678, Count: 50, Chars: 500, Share: 20},
		},
		Words:  []stats.Named{{Name: "hello", Count: 40}, {Name: "world", Count: 20}},
		Emojis: []stats.Named{{Name: "kekw", Count: 5}},
	}
	s.Heat[2][23] = 120
	return s
}

func TestRenderPage(t *testing.T) {
	out, err := Render(sample(), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)

	for _, want := range []string{
		"<!doctype html>", "Test &lt;Guild&gt;", "alice***", "bob***",
		"12,345", "general", "hello", "kekw", "Messages per day", "collector up 97%",
		"https://cdn.discordapp.com/avatars/123456789012345678/abc123.png?size=64",
		"https://cdn.discordapp.com/embed/avatars/",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	// Pseudonymisation: nothing that looks like a Discord snowflake or a mention leaks.
	if strings.Contains(html, "<@") {
		t.Error("mention markup leaked into the page")
	}
	// The only NaN allowed is the table sorter's own isNaN(); anything else means
	// a chart divided by zero.
	if n, ok := strings.Count(html, "NaN"), strings.Count(html, "isNaN"); n != ok {
		t.Errorf("%d NaN values but only %d in isNaN(): the renderer produced NaN", n, ok)
	}
	for _, bad := range []string{">Inf", "-Inf", "+Inf"} {
		if i := strings.Index(html, bad); i >= 0 {
			lo := max(0, i-90)
			t.Errorf("%q at %d: ...%s...", bad, i, html[lo:i+40])
		}
	}
	if len(out) > 250_000 {
		t.Errorf("page is %d bytes, budget is 250KB", len(out))
	}
}

func TestRenderEmpty(t *testing.T) {
	out, err := Render(stats.Stats{GuildID: 1, GuildName: "Quiet", Generated: time.Now()}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("Collecting data")) {
		t.Error("expected the empty state")
	}
	if bytes.Contains(out, []byte("Messages per day")) {
		t.Error("charts must not render without data")
	}
}

// A day with a single message should scale the chart without dividing by zero.
func TestRenderZeroMax(t *testing.T) {
	s := sample()
	s.Daily = []stats.Point{{Day: 1, Count: 0, Up: 0}}
	if _, err := Render(s, time.UTC); err != nil {
		t.Fatal(err)
	}
}
