package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Guild struct {
	ID          int64
	Repo        string // owner/repo
	KeepHistory bool   // #history suffix: append commits instead of force-pushing a single one
}

type Config struct {
	Token       string
	Guilds      []Guild
	GitHubToken string
	Branch      string
	Path        string // optional dir inside the repo, "" = root
	DataDir     string
	Location    *time.Location
	Interval    time.Duration
	TopN        int
	IncludeBots bool
}

func Load() (*Config, error) {
	LoadDotEnv(".env")

	c := &Config{
		Token:       os.Getenv("DISCORD_TOKEN"),
		GitHubToken: os.Getenv("GITHUB_TOKEN"),
		Branch:      env("GITHUB_BRANCH", "main"),
		Path:        strings.Trim(strings.TrimSpace(os.Getenv("PAGES_PATH")), "/"),
		DataDir:     env("DATA_DIR", "/data"),
		TopN:        envInt("TOP_N", 25),
		IncludeBots: envBool("INCLUDE_BOTS"),
	}

	var err error
	if c.Location, err = time.LoadLocation(env("TZ", "Asia/Seoul")); err != nil {
		return nil, fmt.Errorf("TZ: %w", err)
	}
	if c.Interval, err = time.ParseDuration(env("STATS_INTERVAL", "6h")); err != nil || c.Interval <= 0 {
		return nil, fmt.Errorf("STATS_INTERVAL: %q", os.Getenv("STATS_INTERVAL"))
	}
	if c.Guilds, err = parseGuilds(os.Getenv("GUILD_REPOS")); err != nil {
		return nil, err
	}

	var missing []string
	if c.Token == "" {
		missing = append(missing, "DISCORD_TOKEN")
	}
	if c.GitHubToken == "" {
		missing = append(missing, "GITHUB_TOKEN")
	}
	if len(c.Guilds) == 0 {
		missing = append(missing, "GUILD_REPOS")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing env: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// parseGuilds accepts "id:owner/repo" entries separated by commas or newlines,
// each optionally suffixed with #history.
func parseGuilds(raw string) ([]Guild, error) {
	var out []Guild
	seen := map[int64]bool{}
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keep := strings.HasSuffix(entry, "#history")
		entry = strings.TrimSuffix(entry, "#history")

		idStr, repo, ok := strings.Cut(entry, ":")
		if !ok || !strings.Contains(repo, "/") {
			return nil, fmt.Errorf("GUILD_REPOS: bad entry %q, want id:owner/repo", entry)
		}
		id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
		if err != nil || id == 0 {
			return nil, fmt.Errorf("GUILD_REPOS: bad guild id %q", idStr)
		}
		if seen[id] {
			return nil, fmt.Errorf("GUILD_REPOS: duplicate guild id %d", id)
		}
		seen[id] = true
		out = append(out, Guild{ID: id, Repo: strings.TrimSpace(repo), KeepHistory: keep})
	}
	return out, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil && v > 0 {
		return v
	}
	return def
}

func envBool(k string) bool {
	v, _ := strconv.ParseBool(os.Getenv(k))
	return v
}

// loadDotEnv fills unset variables from a .env file. Compose env_file works too;
// this is for `go run` and the spike.
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
