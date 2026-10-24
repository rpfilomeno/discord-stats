# discord-stats

A small Go service that watches Discord servers it is allowed to see and publishes
one static stats page per server to its own GitHub Pages repository.

Live at [tps.rogverse.fyi](https://tps.rogverse.fyi) and friends.

```
gateway ──MESSAGE_CREATE──▶ batch writer ──▶ SQLite ──▶ aggregate ──▶ index.html ──▶ GitHub commit
```

One container, one SQLite file, one HTML file per repo. ~50 MB RAM, ~0% CPU idle.

## Privacy by design

- **Message text is never stored.** Words are counted into a `(word, n)` table and the
  content is dropped immediately; only counts, lengths and flags survive.
- **Names are masked before storage.** The last three characters are replaced with
  `***` at ingest, so the database never holds a full username (`rpfilomeno` becomes
  `rpfilom***`). A per-server alias is still assigned internally so pages stay stable
  if someone renames.
- **Avatars are hotlinked, not copied.** Only the CDN hash is stored; the page points
  the visitor's browser at `cdn.discordapp.com`. Missing avatar → Discord's default.
- **DMs are dropped before anything else.** Only guild messages are seen at all.
- **Only listed guilds are stored.** A guild missing from `GUILD_REPOS` is ignored at
  the event handler: no rows, no page.
- Pages are `noindex`.

The database is still behavioural data about real people. Tell the server admins
before you publish it.

## Requirements

- A Discord **user token** in `.env`. See the warning below.
- One repository per server, **already created**, with Pages set to
  *Deploy from a branch* → `main` → `/`.
- A fine-grained GitHub PAT with **Contents: read/write** on those repositories.

## Setup

```bash
cp .env.example .env      # fill in DISCORD_TOKEN, GUILD_REPOS, GITHUB_TOKEN

go run ./cmd/spike -d 90s # 1. prove the token can read messages
docker compose up -d --build
docker compose logs -f
```

The spike is the important step: it prints the identify payload, every guild it can
see, and a few message events. If it sees no messages, fix that before running the
bot — nothing else in this project is risky.

The first page is published one minute after start; after that every `STATS_INTERVAL`
(default 6 h). A cycle is skipped entirely when the rendered page has not changed.

## Presence (optional)

The account's status is set in the identify payload, so it is already correct the
moment the account appears online — no flicker of a blank presence first.

| Variable | Meaning |
|---|---|
| `STATUS` | `online`, `idle`, `dnd` or `invisible` (default `online`) |
| `CUSTOM_STATUS` | custom status text, max 128 chars |
| `CUSTOM_STATUS_EMOJI` | unicode emoji in front of the text |
| `RPC_NAME` | activity name, e.g. `Visual Studio Code`; empty = no rich presence |
| `RPC_TYPE` | `playing`, `listening`, `watching`, `competing` |
| `RPC_DETAILS`, `RPC_STATE` | the two lines under the name |
| `RPC_IMAGE`, `RPC_IMAGE_TEXT` | large image asset key (or URL) and its hover text |
| `RPC_START_MIN` | minutes already elapsed, so the timer reads "2:14 elapsed" |
| `RPC_APP_ID` | application that owns the image asset |

Custom status and rich presence are two separate activities and are sent together,
the same way the official client does it.

`streaming` is deliberately mapped back to `playing`: it requires a live
Twitch/YouTube URL and the gateway rejects the whole presence without one.

A **bot** account cannot show details, state or images, so this only makes sense with
a user token — which is what this project already uses. `STATUS=invisible` is the
quietest option for an account that is only here to count messages.

## Publishing model

Each publish writes `index.html` and `.nojekyll` in **one commit with no parents** and
force-updates the branch, built on top of the current tree (`base_tree`) so `CNAME`,
`README` and anything else in the repo survive.

Why: appending a fresh 100 KB page every 6 h adds ~175 MB/year of git history, and every
past version of a page about people stays readable forever. This keeps the repo at one
commit and ~100 KB while making old data actually go away.

A repo with history worth keeping can opt out of the squash:

```ini
1124533898764890132:owner/repo#history   # normal append-only commits
```

## Operations

- Data lives in `./data/stats.db` (WAL). It is the only irreplaceable state: the bot
  collects **live only**, so a day it was not running is gone.
- Back up with `sqlite3 data/stats.db "VACUUM INTO 'data/backup.db'"`.
- Shutdown (`SIGTERM`/Ctrl-C) flushes the in-flight batch.
- `docker compose logs -f` shows one line per publish: `changed`, `skipped` or `error`.

## Known limits

| | |
|---|---|
| Live only | no backfill; a stopped container loses that period permanently |
| Edits/deletes/reactions | ignored; a message is counted once, as first seen |
| Emoji | custom emoji only (`<:name:id>`), unicode emoji are not counted |
| Threads | counted, filed under their thread id |
| Member count | approximate, fetched over REST at startup; user sessions never get `GUILD_CREATE` |
| Downtime | days the collector was offline are shaded in the chart, not silently zero |

## Discord ToS

A user token driving a client is against Discord's Terms of Service and can get the
account terminated. This bot is read-only, makes no writes to Discord and avoids the
REST message endpoints, which is the least detectable shape — it is still not a safe
one. A bot account would be compliant; it only changes `cmd/spike` and the identify
payload in `internal/discord`, everything else is identical.

## Code map

```
cmd/spike       connection test / identify debugging
cmd/statsbot    wiring, batch writer, tickers, publish cycle
internal/discord gateway, filters, tokenizer, presence
internal/store   schema, idempotent inserts, aggregate SQL
internal/render  stats struct -> one self-contained HTML page (SVG, no JS libs)
internal/publish GitHub Git Data API
third_party     discordgo v0.29.0 + PATCHES.md (read that file if login breaks)
```
