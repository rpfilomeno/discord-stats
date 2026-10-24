package discord

import (
	"reflect"
	"testing"
)

func TestWords(t *testing.T) {
	got := Words("Hello HELLO world https://example.com/foo <@123> <:kek:456> the a go")
	want := []string{"hello", "hello", "world"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestWordsCap(t *testing.T) {
	var s string
	for i := 0; i < 500; i++ {
		s += "word "
	}
	if n := len(Words(s)); n != 200 {
		t.Fatalf("want 200 tokens, got %d", n)
	}
}

func TestEmojis(t *testing.T) {
	got := Emojis("nice <:kekw:123> and <a:catjam:456>")
	want := []string{"kekw", "catjam"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestHasLink(t *testing.T) {
	if !hasLink("see https://x.dev") || hasLink("no link here") {
		t.Fatal("hasLink broken")
	}
}

func TestMentionStrip(t *testing.T) {
	if got := Words("<@&999> hey"); !reflect.DeepEqual(got, []string{"hey"}) {
		t.Fatalf("got %v", got)
	}
}
