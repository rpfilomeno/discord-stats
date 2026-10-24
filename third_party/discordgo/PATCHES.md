# discordgo patches

Vendored copy of `github.com/bwmarrin/discordgo` v0.29.0, wired in via
`replace github.com/bwmarrin/discordgo => ./third_party/discordgo`.

This file is the only place that differs from upstream. If login stops working,
read this first.

## structs.go — `Identify`

```go
Intents        Intent `json:"intents,omitempty"`          // was json:"intents"
Capabilities   int    `json:"capabilities,omitempty"`     // added
ClientState    any    `json:"client_state,omitempty"`     // added
```

Why: a user token's identify payload has no `intents` (and sending `intents: 0`
may be read literally as "no events"), and the web client sends `capabilities` /
`client_state`. Without these fields they cannot be marshalled at all, because
`wsapi` marshals the struct as-is.

## structs.go — `GatewayStatusUpdate`

```go
Game       *Activity   `json:"game,omitempty"`        // was Game Activity with no omitempty
Activities []*Activity `json:"activities,omitempty"`  // added
```

Why: `Identify.Presence` is marshalled by `wsapi`, and the stock struct can only
carry the legacy single `game` field. A custom status *and* a rich presence need
the modern `activities` array; `game` became a pointer so the zero activity is
not sent at all. `wsapi.UpdateStatusComplex` already uses `activities`, so only
the identify path needed this.

Nothing else is patched. REST auth already sends `Authorization: <s.Token>`
verbatim (`restapi.go`), so a raw user token needs no prefix.

## Rebase checklist

1. `go mod download github.com/bwmarrin/discordgo@<new>`
2. copy `*.go` (minus `_test.go`), `go.mod`, `go.sum`, `LICENSE`
3. re-apply the struct changes above (Identify, GatewayStatusUpdate)
4. `go build ./...`
