package discord

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestPresenceStatusOnly(t *testing.T) {
	p := PresenceFromEnv()
	if p.Status != "online" {
		t.Fatalf("status = %q", p.Status)
	}
	if len(p.Activities) != 0 {
		t.Fatalf("activities = %v", p.Activities)
	}
	if p.Game != nil {
		t.Fatal("legacy game field must stay empty")
	}
}

func TestPresenceStatusMapping(t *testing.T) {
	for _, s := range []string{"online", "idle", "dnd", "invisible"} {
		t.Setenv("STATUS", s)
		if got := PresenceFromEnv().Status; got != s {
			t.Errorf("STATUS=%q -> %q", s, got)
		}
	}
	for _, s := range []string{"DND", " idle ", "bogus"} {
		t.Setenv("STATUS", s)
		got := PresenceFromEnv().Status
		want := strings.ToLower(strings.TrimSpace(s))
		if want != "idle" && want != "dnd" {
			want = "online"
		}
		if got != want {
			t.Errorf("STATUS=%q -> %q, want %q", s, got, want)
		}
	}
}

func TestPresenceCustomStatus(t *testing.T) {
	t.Setenv("CUSTOM_STATUS", "  grinding  ")
	t.Setenv("CUSTOM_STATUS_EMOJI", "🔥")
	p := PresenceFromEnv()
	if len(p.Activities) != 1 {
		t.Fatalf("activities = %d", len(p.Activities))
	}
	a := p.Activities[0]
	if a.Type != discordgo.ActivityTypeCustom || a.Name != "Custom Status" || a.State != "grinding" {
		t.Fatalf("bad custom status: %+v", a)
	}
	if a.Emoji.Name != "🔥" {
		t.Fatalf("emoji = %q", a.Emoji.Name)
	}
}

func TestPresenceRPCAndCustomCoexist(t *testing.T) {
	t.Setenv("STATUS", "idle")
	t.Setenv("CUSTOM_STATUS", "afk")
	t.Setenv("RPC_NAME", "Visual Studio Code")
	t.Setenv("RPC_TYPE", "watching")
	t.Setenv("RPC_DETAILS", "editing main.go")
	t.Setenv("RPC_STATE", "workspace: discord-stats")
	t.Setenv("RPC_IMAGE", "vscode")
	t.Setenv("RPC_IMAGE_TEXT", "VS Code")
	t.Setenv("RPC_START_MIN", "90")

	p := PresenceFromEnv()
	if p.Status != "idle" {
		t.Fatalf("status = %q", p.Status)
	}
	if len(p.Activities) != 2 {
		t.Fatalf("want custom status + rpc, got %d", len(p.Activities))
	}
	rpc := p.Activities[1]
	if rpc.Type != discordgo.ActivityTypeWatching || rpc.Name != "Visual Studio Code" {
		t.Fatalf("bad activity: %+v", rpc)
	}
	if rpc.Details != "editing main.go" || rpc.State != "workspace: discord-stats" {
		t.Fatalf("bad text: %+v", rpc)
	}
	if rpc.Assets.LargeImageID != "vscode" || rpc.Assets.LargeText != "VS Code" {
		t.Fatalf("bad assets: %+v", rpc.Assets)
	}
	want := time.Now().Add(-90 * time.Minute).UnixMilli()
	if d := rpc.Timestamps.StartTimestamp - want; d < -2000 || d > 2000 {
		t.Fatalf("start timestamp off by %d ms", d)
	}
}

func TestPresenceRPCTypes(t *testing.T) {
	cases := map[string]discordgo.ActivityType{
		"playing":   discordgo.ActivityTypeGame,
		"listening": discordgo.ActivityTypeListening,
		"watching":  discordgo.ActivityTypeWatching,
		"competing": discordgo.ActivityTypeCompeting,
		"streaming": discordgo.ActivityTypeGame, // no URL -> plain playing
		"":          discordgo.ActivityTypeGame,
	}
	for in, want := range cases {
		t.Setenv("RPC_TYPE", in)
		t.Setenv("RPC_NAME", "x")
		if got := PresenceFromEnv().Activities[0].Type; got != want {
			t.Errorf("RPC_TYPE=%q -> %v, want %v", in, got, want)
		}
	}
}

func TestPresenceJSON(t *testing.T) {
	t.Setenv("CUSTOM_STATUS", "afk")
	t.Setenv("RPC_NAME", "Terminal")
	raw, err := json.Marshal(PresenceFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	js := string(raw)
	for _, want := range []string{`"activities":[`, `"status":"online"`, `"type":4`, `"name":"Terminal"`} {
		if !strings.Contains(js, want) {
			t.Errorf("payload missing %s: %s", want, js)
		}
	}
	if strings.Contains(js, `"game"`) {
		t.Errorf("legacy game field leaked into the payload: %s", js)
	}
	if strings.Contains(js, "0001-01-01") {
		t.Errorf("zero created_at in the payload: %s", js)
	}
}

func TestPresenceTruncates(t *testing.T) {
	t.Setenv("CUSTOM_STATUS", strings.Repeat("a", 300))
	t.Setenv("RPC_NAME", strings.Repeat("b", 300))
	p := PresenceFromEnv()
	if n := len([]rune(p.Activities[0].State)); n != maxPresenceText {
		t.Fatalf("custom status = %d runes", n)
	}
	if n := len([]rune(p.Activities[1].Name)); n != maxPresenceText {
		t.Fatalf("rpc name = %d runes", n)
	}
}
