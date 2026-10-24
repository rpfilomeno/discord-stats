package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rpfilomeno/discord-stats/internal/config"
	"github.com/rpfilomeno/discord-stats/internal/discord"
	"github.com/rpfilomeno/discord-stats/internal/publish"
	"github.com/rpfilomeno/discord-stats/internal/render"
	"github.com/rpfilomeno/discord-stats/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel(os.Getenv("LOG_LEVEL")),
	})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	for _, g := range cfg.Guilds {
		slog.Info("publish target", "guild", g.ID, "repo", g.Repo, "keep_history", g.KeepHistory)
	}

	st, err := store.Open(cfg.DataDir, cfg.Location)
	if err != nil {
		slog.Error("store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	in := make(chan store.Message, 4096)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		writer(ctx, st, in)
	}()

	client, err := discord.New(cfg, st, func(m store.Message) {
		select {
		case in <- m:
		default:
			slog.Warn("ingest queue full, dropping message", "guild", m.GuildID)
		}
	})
	if err != nil {
		slog.Error("discord", "err", err)
		os.Exit(1)
	}
	go func() {
		if err := client.Run(ctx); err != nil {
			slog.Error("gateway stopped", "err", err)
			stop()
		}
	}()

	pub := publish.New(cfg.GitHubToken, cfg.Branch, cfg.Path)
	go uptimeLoop(ctx, st, cfg.Location)
	go publishLoop(ctx, cfg, st, pub, time.Minute)

	<-ctx.Done()
	slog.Info("shutting down")
	wg.Wait()
}

func writer(ctx context.Context, st *store.Store, in <-chan store.Message) {
	const batch = 200
	buf := make([]store.Message, 0, batch)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		if err := st.InsertBatch(buf); err != nil {
			slog.Error("insert batch", "n", len(buf), "err", err)
		}
		buf = buf[:0]
	}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case m := <-in:
			buf = append(buf, m)
			if len(buf) >= batch {
				flush()
			}
		case <-tick.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case m := <-in:
					buf = append(buf, m)
				default:
					flush()
					return
				}
			}
		}
	}
}

func uptimeLoop(ctx context.Context, st *store.Store, loc *time.Location) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if err := st.TickUptime(store.DayNumber(time.Now(), loc)); err != nil {
			slog.Error("uptime", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func publishLoop(ctx context.Context, cfg *config.Config, st *store.Store, pub *publish.Client, first time.Duration) {
	firstRun := time.NewTimer(first)
	defer firstRun.Stop()
	tick := time.NewTicker(cfg.Interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-firstRun.C:
		case <-tick.C:
		}
		cycle(ctx, cfg, st, pub)
	}
}

func cycle(ctx context.Context, cfg *config.Config, st *store.Store, pub *publish.Client) {
	start := time.Now()
	now := time.Now()
	for _, g := range cfg.Guilds {
		s, err := st.BuildStats(g.ID, cfg.TopN, 365, now)
		if err != nil {
			slog.Error("aggregate", "guild", g.ID, "err", err)
			continue
		}
		page, err := render.Render(s, cfg.Location)
		if err != nil {
			slog.Error("render", "guild", g.ID, "err", err)
			continue
		}

		sum := sha256.Sum256(page)
		hash := hex.EncodeToString(sum[:])
		if last, _ := st.LastHash(g.ID); last == hash {
			slog.Info("unchanged, skipped", "guild", g.ID, "repo", g.Repo)
			continue
		}

		files := map[string][]byte{
			"index.html": page,
			".nojekyll":  nil,
		}
		msg := "Update stats — " + now.In(cfg.Location).Format("2 Jan 2006 15:04 MST")
		sha, err := pub.Publish(ctx, g.Repo, files, msg, !g.KeepHistory)
		if err != nil {
			slog.Error("publish", "guild", g.ID, "repo", g.Repo, "err", err)
			continue
		}
		if err := st.SetLastHash(g.ID, hash); err != nil {
			slog.Error("save hash", "guild", g.ID, "err", err)
		}
		slog.Info("published", "guild", g.ID, "repo", g.Repo, "commit", sha[:7],
			"messages", s.Total, "bytes", len(page))
	}

	ids := make([]int64, 0, len(cfg.Guilds))
	for _, g := range cfg.Guilds {
		ids = append(ids, g.ID)
	}
	if err := st.PruneAll(ids, 2000); err != nil {
		slog.Error("prune", "err", err)
	}
	slog.Info("cycle done", "took", time.Since(start).Round(time.Millisecond))
}

func logLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}
