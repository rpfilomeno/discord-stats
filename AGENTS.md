# AGENTS.md

Go service that reads Discord guild messages over the gateway and publishes one static
HTML page per guild to a GitHub Pages repo. Read `README.md` first; this file is the
stuff that bites.

## Commands

```bash
go build ./...
go test ./...
go test ./internal/store -run TestInsertAndAggregate -count=1   # one test

go run ./cmd/spike -d 90s      # needs DISCORD_TOKEN; prints identify payload + guilds
docker compose up -d --build   # production
gofmt -w cmd internal          # NOT third_party
```

No linter, no codegen, no CI. Tests need no services and no network (GitHub is faked
with `httptest`).

## Architecture

`cmd/statsbot` wires it: gateway → 4096-message channel → batch writer (1 s / 200
msgs) → SQLite → `BuildStats` → `Render` → commit. Everything is one process, one DB,
one publish loop.

- `internal/store` owns the schema and all aggregate SQL; it imports `internal/stats`
  (plain types), which `internal/render` consumes. Don't move aggregation elsewhere.
- `internal/discord` filters events before they reach the writer: guild allowlist,
  DMs (`GuildID == ""`), bots.
- `internal/publish` talks to the Git Data API only (blobs → tree → commit → ref).

## Invariants — breaking these is a bug, not a preference

- **Mask at ingest, not at render.** `MaskName` runs in the gateway handler before the
  row exists; the DB never holds a full username. Same for avatars: only the CDN hash
  is stored, the URL is rebuilt in `render.avatarURL`.
- **Message text never reaches SQLite.** Only word/emoji counts, lengths, flags.
- **Insert is idempotent.** `messages` uses `ON CONFLICT(id) DO NOTHING` and counters
  are only bumped when `RowsAffected() > 0`. Removing that double-counts on reconnect.
- **Day buckets are local calendar days** via `store.DayNumber`. Never `ts / 86400` —
  it breaks the chart across DST.
- **Publishing squashes by default**: one parentless commit + `base_tree` + force ref,
  so old page versions (and the people in them) don't stay in git history. `#history`
  in `GUILD_REPOS` switches to normal appends. `internal/publish` tests assert both.
- **The page is one self-contained file** (inline CSS, Go-built SVG, ~40 lines of
  vanilla JS). No CDN fonts, no JS libraries; a render test fails past 250 KB.

## Vendored discordgo (the biggest trap)

`third_party/discordgo` is discordgo v0.29.0 with local patches, wired by `replace` in
`go.mod`. It is not a normal dependency:

- Read `third_party/discordgo/PATCHES.md` before touching it; document every new patch
  there. The patches add the user-token identify fields (`capabilities`,
  `client_state`, optional `intents`) and `activities` on `GatewayStatusUpdate`.
- Files copied out of the module cache are **read-only**: `attrib -R third_party\discordgo\*.* /S`
  before editing, or the edit tool fails with no useful message.
- Do not run `gofmt`/`go mod tidy`-driven reformatting across `third_party`; it produces
  a huge diff against upstream.

## Gotchas

- **A user session never receives `GUILD_CREATE`.** Discord sends READY plus
  READY_SUPPLEMENTAL (guild ids, merged members, presences) and then normal per-guild
  events; guild names and channels therefore come from REST in `hydrateGuilds`. Do not
  wait for `GUILD_CREATE` or "fix" the identify payload because it is missing.
- **No migrations.** Schema is `CREATE TABLE IF NOT EXISTS`; changing a table's columns
  does nothing to an existing DB. Delete `./data/stats.db` (dev only) after schema edits.
- `go.mod` requires Go 1.26.5 and the Dockerfile pins `golang:1.26.5-alpine` — bump both
  together.
- `docker build` prints `Exited with code 1` in PowerShell even when it succeeds.
  Check for `DONE` / `naming to docker.io/...` instead of the exit code.
- `.env` must be UTF-8 without BOM (PowerShell 5.1 `Set-Content` writes UTF-16 and
  corrupts the first key). `config.LoadDotEnv` only fills variables that are unset, so a
  real env var wins over `.env`.
- `.env` holds a **live user token**: never print it, commit it, or copy it into logs,
  tests or fixtures. `.env` and `data/` are gitignored; keep it that way.
- The identify payload is only exercised by `go run ./cmd/spike -d 90s`; there is no
  recorded proof it works against live Discord. If login fails, that command is the
  diagnostic (it prints the payload with the token redacted).
- Only two direct deps (`discordgo`, `modernc.org/sqlite`, CGO disabled). Don't add a
  third for anything the stdlib can do.
