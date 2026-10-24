package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/rpfilomeno/discord-stats/internal/config"
	"github.com/rpfilomeno/discord-stats/internal/store"
)

// browserUA keeps the one REST call we make (GET /gateway) from announcing the
// library. It is not a claim of being the official client.
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// NewSession builds a gateway session with a user-account identify payload.
// The extra fields only exist because of third_party/discordgo/PATCHES.md.
func NewSession(token string) (*discordgo.Session, error) {
	s, err := discordgo.New(token)
	if err != nil {
		return nil, err
	}
	s.StateEnabled = false // no member cache in RAM; we track guilds/channels ourselves
	s.UserAgent = browserUA
	s.Identify.Token = token
	s.Identify.Compress = false
	s.Identify.Intents = 0
	s.Identify.LargeThreshold = 250
	s.Identify.Capabilities = 30717
	s.Identify.ClientState = map[string]any{
		"guild_versions":              map[string]any{},
		"highest_last_message_id":     "0",
		"read_state_version":          0,
		"user_guild_settings_version": -1,
		"user_settings_version":       -1,
	}
	s.Identify.Properties = discordgo.IdentifyProperties{
		OS:      "Windows",
		Browser: "Chrome",
		Device:  "",
	}
	s.Identify.Presence = PresenceFromEnv()
	return s, nil
}

type Client struct {
	sess   *discordgo.Session
	st     *store.Store
	guilds map[int64]config.Guild
	bots   bool
	emit   func(store.Message)

	mu       sync.RWMutex
	channels map[int64]string
}

func New(cfg *config.Config, st *store.Store, emit func(store.Message)) (*Client, error) {
	sess, err := NewSession(cfg.Token)
	if err != nil {
		return nil, err
	}
	c := &Client{
		sess:     sess,
		st:       st,
		guilds:   map[int64]config.Guild{},
		bots:     cfg.IncludeBots,
		emit:     emit,
		channels: map[int64]string{},
	}
	for _, g := range cfg.Guilds {
		c.guilds[g.ID] = g
	}

	sess.AddHandler(c.onGuildCreate)
	sess.AddHandler(c.onChannelCreate)
	sess.AddHandler(c.onMessageCreate)
	return c, nil
}

func (c *Client) Run(ctx context.Context) error {
	if err := c.sess.Open(); err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	defer c.sess.Close()

	slog.Info("gateway connected", "guilds", len(c.guilds))
	c.hydrateGuilds()
	<-ctx.Done()
	return nil
}

// hydrateGuilds reads guild metadata over REST. A user-account session gets
// READY plus READY_SUPPLEMENTAL and never a GUILD_CREATE, so the gateway alone
// never reveals guild names or channels.
func (c *Client) hydrateGuilds() {
	for id := range c.guilds {
		sid := strconv.FormatInt(id, 10)
		g, err := c.sess.GuildWithCounts(sid) // ?with_counts=true is the only place member counts live
		if err != nil {
			slog.Error("fetch guild", "guild", id, "err", err)
			continue
		}
		members := int64(g.ApproximateMemberCount)
		if err := c.st.EnsureGuild(id, g.Name, g.Icon, members); err != nil {
			slog.Error("ensure guild", "guild", id, "err", err)
		}
		chans, err := c.sess.GuildChannels(sid)
		if err != nil {
			slog.Error("fetch channels", "guild", id, "err", err)
			continue
		}
		for _, ch := range chans {
			c.trackChannel(id, ch)
		}
		slog.Info("guild ready", "id", id, "name", g.Name, "channels", len(chans), "members", members)
	}
}

func (c *Client) onGuildCreate(_ *discordgo.Session, g *discordgo.GuildCreate) {
	if g.Unavailable {
		return
	}
	id, err := strconv.ParseInt(g.ID, 10, 64)
	if err != nil {
		return
	}
	if _, ok := c.guilds[id]; !ok {
		return // not in GUILD_REPOS: ignore entirely
	}
	if err := c.st.EnsureGuild(id, g.Name, g.Icon, int64(g.MemberCount)); err != nil {
		slog.Error("ensure guild", "guild", id, "err", err)
	}
	for _, ch := range g.Channels {
		c.trackChannel(id, ch)
	}
	slog.Info("guild ready", "id", id, "name", g.Name, "channels", len(g.Channels), "members", g.MemberCount)
}

func (c *Client) onChannelCreate(_ *discordgo.Session, ch *discordgo.ChannelCreate) {
	if ch.GuildID == "" {
		return
	}
	gid, err := strconv.ParseInt(ch.GuildID, 10, 64)
	if err != nil {
		return
	}
	c.trackChannel(gid, ch.Channel)
}

func (c *Client) trackChannel(guildID int64, ch *discordgo.Channel) {
	if _, ok := c.guilds[guildID]; !ok || ch == nil {
		return
	}
	id, err := strconv.ParseInt(ch.ID, 10, 64)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.channels[id] = ch.Name
	c.mu.Unlock()
	if err := c.st.EnsureChannel(id, guildID, ch.Name, int64(ch.Type)); err != nil {
		slog.Error("ensure channel", "channel", id, "err", err)
	}
}

func (c *Client) onMessageCreate(_ *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.GuildID == "" || m.Author == nil {
		return // DMs and system events never reach the DB
	}
	gid, err := strconv.ParseInt(m.GuildID, 10, 64)
	if err != nil {
		return
	}
	if _, ok := c.guilds[gid]; !ok {
		return
	}
	if m.Author.Bot && !c.bots {
		return
	}
	mid, err1 := strconv.ParseInt(m.ID, 10, 64)
	cid, err2 := strconv.ParseInt(m.ChannelID, 10, 64)
	aid, err3 := strconv.ParseInt(m.Author.ID, 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return
	}

	ts := m.Timestamp
	if ts.IsZero() {
		ts = time.UnixMilli((mid >> 22) + 1420070400000)
	}

	name := m.Author.GlobalName
	if name == "" {
		name = m.Author.Username
	}

	msg := store.Message{
		ID:           mid,
		GuildID:      gid,
		ChannelID:    cid,
		AuthorID:     aid,
		AuthorName:   MaskName(name),
		AuthorAvatar: m.Author.Avatar,
		TS:           ts.Unix(),
		Chars:        int64(len([]rune(m.Content))),
		Attachments:  int64(len(m.Attachments)),
		HasLink:      hasLink(m.Content),
		IsReply:      m.MessageReference != nil,
		Words:        Words(m.Content),
		Emojis:       Emojis(m.Content),
	}
	c.emit(msg)
}
