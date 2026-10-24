package store

import (
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), time.FixedZone("PST", 8*3600))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndAggregate(t *testing.T) {
	s := newStore(t)
	loc := time.FixedZone("PST", 8*3600)
	ts := time.Date(2026, 10, 20, 23, 30, 0, 0, loc).Unix() // Tuesday

	if err := s.EnsureGuild(1, "Test Guild", "iconhash", 42); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureChannel(10, 1, "general", 0); err != nil {
		t.Fatal(err)
	}

	msgs := []Message{
		{ID: 1001, GuildID: 1, ChannelID: 10, AuthorID: 100, AuthorName: "alice***", AuthorAvatar: "abc123", TS: ts, Chars: 10, Words: []string{"hello", "hello"}},
		{ID: 1002, GuildID: 1, ChannelID: 10, AuthorID: 100, TS: ts, Chars: 20, HasLink: true, Words: []string{"hello", "world"}},
		{ID: 1003, GuildID: 1, ChannelID: 10, AuthorID: 200, AuthorName: "bob***", TS: ts, Chars: 30, Attachments: 1, Emojis: []string{"kekw"}},
		{ID: 1001, GuildID: 1, ChannelID: 10, AuthorID: 100, TS: ts, Chars: 10, Words: []string{"hello"}}, // duplicate
	}
	if err := s.InsertBatch(msgs); err != nil {
		t.Fatal(err)
	}

	st, err := s.BuildStats(1, 10, 365, time.Date(2026, 10, 21, 12, 0, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}

	if st.Total != 3 || st.Active != 2 {
		t.Fatalf("total/active = %d/%d, want 3/2", st.Total, st.Active)
	}
	if st.TotalChars != 60 {
		t.Fatalf("chars = %d, want 60", st.TotalChars)
	}
	if len(st.MembersTop) != 2 || st.MembersTop[0].Alias != 1 || st.MembersTop[0].Count != 2 {
		t.Fatalf("members = %+v", st.MembersTop)
	}
	if st.MembersTop[0].Name != "alice***" || st.MembersTop[0].Avatar != "abc123" {
		t.Fatalf("member = %+v", st.MembersTop[0])
	}
	if st.MembersTop[1].Alias != 2 {
		t.Fatalf("second alias should be stable at 2, got %d", st.MembersTop[1].Alias)
	}
	if st.Heat[time.Tuesday][23] != 3 {
		t.Fatalf("heat[tue][23] = %d, want 3", st.Heat[time.Tuesday][23])
	}
	if len(st.Channels) != 1 || st.Channels[0].Name != "general" || st.Channels[0].Count != 3 {
		t.Fatalf("channels = %+v", st.Channels)
	}
	// "hello" appears 3 times across distinct messages: twice in 1001, once in 1002.
	for _, w := range st.Words {
		if w.Name == "hello" && w.Count != 3 {
			t.Fatalf("hello = %d, want 3", w.Count)
		}
	}
	var dailySum int64
	for _, d := range st.Daily {
		dailySum += d.Count
	}
	if dailySum != 3 {
		t.Fatalf("daily = %+v", st.Daily)
	}
	if st.PctLink == 0 || st.PctFile == 0 {
		t.Fatalf("rates = %v/%v", st.PctLink, st.PctFile)
	}
}

func TestDuplicateBatchIsIdempotent(t *testing.T) {
	s := newStore(t)
	ts := time.Now().Unix()
	m := Message{ID: 5000, GuildID: 1, ChannelID: 10, AuthorID: 100, TS: ts, Words: []string{"once"}}
	if err := s.InsertBatch([]Message{m}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertBatch([]Message{m}); err != nil {
		t.Fatal(err)
	}
	st, err := s.BuildStats(1, 10, 365, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 1 {
		t.Fatalf("total = %d, want 1", st.Total)
	}
	if len(st.Words) != 1 || st.Words[0].Count != 1 {
		t.Fatalf("words = %+v", st.Words)
	}
}

func TestEmptyGuild(t *testing.T) {
	s := newStore(t)
	st, err := s.BuildStats(7, 10, 365, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 0 || len(st.Daily) != 0 {
		t.Fatalf("expected empty stats, got %+v", st)
	}
}

func TestHashRoundTrip(t *testing.T) {
	s := newStore(t)
	if err := s.EnsureGuild(1, "G", "", 0); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.LastHash(1); h != "" {
		t.Fatalf("expected empty hash, got %q", h)
	}
	if err := s.SetLastHash(1, "abc"); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.LastHash(1); h != "abc" {
		t.Fatalf("hash = %q", h)
	}
}

// DayNumber must follow local calendar days across a DST transition, otherwise
// the daily chart gains or loses an hour of messages.
func TestDayNumberDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	before := time.Date(2026, 3, 8, 1, 30, 0, 0, ny) // EST
	after := time.Date(2026, 3, 8, 23, 30, 0, 0, ny) // EDT, same local day
	if DayNumber(before, ny) != DayNumber(after, ny) {
		t.Fatal("same local day must map to the same day number")
	}
	next := time.Date(2026, 3, 9, 0, 30, 0, 0, ny)
	if DayNumber(next, ny)-DayNumber(after, ny) != 1 {
		t.Fatal("consecutive local days must differ by exactly 1 across DST")
	}
}

func TestPruneAll(t *testing.T) {
	s := newStore(t)
	msgs := make([]Message, 0, 10)
	for i := 1; i <= 10; i++ {
		msgs = append(msgs, Message{
			ID: int64(i), GuildID: 1, ChannelID: 10, AuthorID: 1, TS: int64(1700000000 + i),
			Words: []string{string(rune('a' + i))},
		})
	}
	if err := s.InsertBatch(msgs); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneAll([]int64{1}, 3); err != nil {
		t.Fatal(err)
	}
	st, err := s.BuildStats(1, 100, 365, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Words) > 3 {
		t.Fatalf("prune left %d words", len(st.Words))
	}
	if len(st.Words) == 0 {
		t.Fatal("prune removed everything")
	}
}
