package discord

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	urlRe   = regexp.MustCompile(`https?://\S+`)
	emojiRe = regexp.MustCompile(`<(a?):([A-Za-z0-9_~]+):(\d+)>`)
	// longest prefix first: Go's Replacer compares in argument order
	mentions = strings.NewReplacer("<@&", " ", "<@", " ", "<#", " ")
)

func hasLink(s string) bool { return urlRe.MatchString(s) }

// Emojis returns custom emoji names from <:name:id> / <a:name:id> markup.
func Emojis(s string) []string {
	if !strings.Contains(s, "<") {
		return nil
	}
	matches := emojiRe.FindAllStringSubmatch(s, 40)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[2])
	}
	return out
}

// Words lowercases, drops URLs/mentions/emoji markup and stopwords, and keeps
// at most 200 tokens per message so one wall of text cannot skew the cloud.
func Words(s string) []string {
	if s == "" {
		return nil
	}
	s = urlRe.ReplaceAllString(s, " ")
	s = emojiRe.ReplaceAllString(s, " ")
	s = mentions.Replace(s)

	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '’'
	}) {
		if len(out) >= 200 {
			break
		}
		w := strings.ToLower(strings.Trim(f, "'’"))
		if len(w) < 3 || isDigits(w) || stopwords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// MaskName hides the last three characters, keeping at least one visible rune:
// "rpfilomeno" -> "rpfilom***", "abc" -> "a***".
func MaskName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) > 4 {
		return string(r[:len(r)-3]) + "***"
	}
	return string(r[:1]) + "***"
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

var stopwords = func() map[string]bool {
	list := `the and for you are that with this have not but was all can just from they
		what when your about into than then them these there here were will would could
		should been being very much some any more most other only also how who why its
		it's don't doesnt didn't isn't aren't wasn't werent i'm im ive you're youre
		he's she's we're they're let's thats what's who's gonna wanna gotta yeah yep
		nah lol lmao haha hahaha ok okay oh ah uhh umm hmm huh bro bruh dude
		ang mga sa ng na at ay ako ikaw siya kami tayo kayo sila naman lang yung
		para hindi oo po ba mo ko ni si din rin daw kasi pero kung may wala meron`
	m := map[string]bool{}
	for _, w := range strings.Fields(list) {
		m[w] = true
	}
	return m
}()
