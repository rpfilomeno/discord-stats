package config

import "testing"

func TestParseGuilds(t *testing.T) {
	got, err := parseGuilds("1124533898764890132:rpfilomeno/tps,\n" +
		" 956942809984806933:rpfilomeno/lomi-discord-stats#history \n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].ID != 1124533898764890132 || got[0].Repo != "rpfilomeno/tps" || got[0].KeepHistory {
		t.Fatalf("bad first entry: %+v", got[0])
	}
	if !got[1].KeepHistory || got[1].Repo != "rpfilomeno/lomi-discord-stats" {
		t.Fatalf("bad second entry: %+v", got[1])
	}

	for _, bad := range []string{"123", "123:noowner", "abc:o/r", "1:o/r,1:o/r2"} {
		if _, err := parseGuilds(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestParseGuildsEmpty(t *testing.T) {
	got, err := parseGuilds("")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}
