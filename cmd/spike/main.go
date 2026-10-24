// Command spike verifies that a user token can connect to the gateway and read
// messages. Run it before anything else; if it prints messages, the rest works.
//
//	go run ./cmd/spike -d 90s
package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/rpfilomeno/discord-stats/internal/config"
	"github.com/rpfilomeno/discord-stats/internal/discord"
)

func main() {
	config.LoadDotEnv(".env")
	dur := flag.Duration("d", 90*time.Second, "how long to listen")
	debug := flag.Bool("debug", false, "log every gateway frame")
	caps := flag.Int("caps", -1, "override identify capabilities (-1 keeps the built-in value)")
	nostate := flag.Bool("nostate", false, "omit client_state from identify")
	flag.Parse()

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		slog.Error("DISCORD_TOKEN is empty (put it in .env)")
		os.Exit(1)
	}

	s, err := discord.NewSession(token)
	if err != nil {
		slog.Error("session", "err", err)
		os.Exit(1)
	}
	if *debug {
		s.LogLevel = discordgo.LogDebug
	}
	if *caps >= 0 {
		s.Identify.Capabilities = *caps
	}
	if *nostate {
		s.Identify.ClientState = nil
	}

	redacted := s.Identify
	redacted.Token = "<redacted>" // the struct's own token field, not a wrapper
	payload, _ := json.Marshal(redacted)
	slog.Info("identify payload", "json", string(payload))

	counts := map[string]int{}
	guilds := map[string]string{}

	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		slog.Info("READY", "user", r.User.Username, "guilds", len(r.Guilds))
		for _, g := range r.Guilds {
			guilds[g.ID] = g.Name
			slog.Info("  guild", "id", g.ID, "name", g.Name)
		}
	})
	s.AddHandler(func(_ *discordgo.Session, g *discordgo.GuildCreate) {
		guilds[g.ID] = g.Name
		slog.Info("GUILD_CREATE", "id", g.ID, "name", g.Name,
			"channels", len(g.Channels), "members", g.MemberCount)
	})
	s.AddHandler(func(_ *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author == nil {
			return
		}
		counts[m.GuildID]++
		if counts[m.GuildID] <= 3 {
			slog.Info("MESSAGE_CREATE", "guild", m.GuildID, "channel", m.ChannelID,
				"author", m.Author.Username, "chars", len(m.Content))
		}
	})
	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Resumed) { slog.Info("RESUMED") })

	if err := s.Open(); err != nil {
		slog.Error("open", "err", err)
		os.Exit(1)
	}
	defer s.Close()

	slog.Info("listening", "for", dur.String())
	time.Sleep(*dur)

	ids := make([]string, 0, len(guilds))
	for id := range guilds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := strconv.ParseInt(ids[i], 10, 64)
		b, _ := strconv.ParseInt(ids[j], 10, 64)
		return a < b
	})
	for _, id := range ids {
		slog.Info("result", "guild", id, "name", guilds[id], "messages", counts[id])
	}
	if len(counts) == 0 {
		slog.Warn("no messages seen. User sessions get READY + READY_SUPPLEMENTAL and no GUILD_CREATE, so an idle server looks the same as a bad payload; use -debug to check that frames arrive.")
	}
}
