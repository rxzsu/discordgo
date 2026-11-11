# DiscordGo (fork)

[![CI](https://github.com/rxzsu/discordgo/actions/workflows/ci.yml/badge.svg)](https://github.com/rxzsu/discordgo/actions/workflows/ci.yml) [![Go Reference](https://pkg.go.dev/badge/github.com/rxzsu/discordgo.svg)](https://pkg.go.dev/github.com/rxzsu/discordgo) [![Go Report Card](https://goreportcard.com/badge/github.com/rxzsu/discordgo)](https://goreportcard.com/report/github.com/rxzsu/discordgo)

<img align="right" alt="DiscordGo logo" src="docs/img/discordgo.svg" width="400">

This is an actively maintained fork of [bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) —
a [Go](https://golang.org/) package providing low-level bindings to the
[Discord](https://discord.com/) API (REST, Gateway websocket, and voice).

It keeps tracking upstream improvements while adding its own modernizations
and fixes on top. See **[WHY-FORK.md](WHY-FORK.md)** for the motivation and
measured numbers, and [CHANGELOG.md](CHANGELOG.md) for the full list of
differences from upstream.

## Differences from upstream (short version)

- **Go 1.27.4+** required; `github.com/coder/websocket` instead of the
  unmaintained `gorilla/websocket`.
- **Fixed spurious gateway reconnects** caused by a broken heartbeat-ACK
  timeout calculation in upstream.
- **Context-aware REST layer**: rate limit waits and 5xx retries abort as
  soon as the request context is cancelled or times out.
- **New request options**: `WithTimeout(d)`, `WithQuery(k, v)` — per-request
  timeout and query parameters for any REST method.
- **`RESTError.Code()`** — typed access to Discord API error codes.
- **Poll API methods**: `ChannelPollAnswerVoters`, `ChannelPollExpire`.
- **Voice reconnect loop stops** when the connection was manually
  disconnected instead of retrying forever.
- **Rate limiter**: context-aware bucket locking (`LockBucketContext`) and
  automatic pruning of stale buckets.
- **ShardManager** (`sharding.go`): multi-shard gateway support with
  shared handlers, automatic shard count/identify-delay discovery —
  upstream has none. See `examples/sharding`.
- **Cancellable startup**: `Session.OpenContext(ctx)` and
  `ShardManager.OpenContext(ctx)` cancel gateway discovery and handshakes;
  the manager also cancels identify delays and closes partially started
  shards. Existing `Open()` methods remain available. After a successful
  startup, use `Close()` to disconnect.
- **Per-shard configuration**: `ShardingConfig.ConfigureSession` sets
  intents, HTTP clients and other session options before connecting. It
  also configures the temporary session used for gateway discovery.
- **`GuildBulkBan`**: ban up to 200 users in a single API call.
- **Stickers, guild widget, soundboard**: full REST coverage of APIs
  that upstream defined endpoints for but never implemented methods for.
- **Guild Incident Actions (raid panic mode)**: `GuildIncidentActionsEdit`
  and `GuildWithIncidentsData` — temporarily disable invites/DMs during a
  raid and read the current `IncidentsData`.
- **Extended audit log filters**: `GuildAuditLogsComplex` supports the full
  query set, including `after` and `application_id`.
- **Guild Member Verification API**: `GuildMemberVerification` and
  `GuildMemberVerificationEdit` read and update a community guild's
  verification form.
- **Guild Welcome Screen and Vanity URL APIs**: `GuildWelcomeScreen` /
  `GuildWelcomeScreenEdit` and `GuildVanityURL` / `GuildVanityURLEdit`
  manage the community welcome screen and the custom invite code.
- **Partial onboarding edits**: `GuildOnboardingEditPartial` updates only
  the supplied onboarding fields (prompts, default channels, enabled flag)
  via `?partial=true` instead of rewriting the whole flow.
- **Typed event handlers (generics)**: `AddHandlerTyped` /
  `AddHandlerOnceTyped` accept `func(*Session, *E)` with the event type as
  a compile-time generic parameter — a handler registered for the wrong
  event type is a compile error instead of a handler that silently never
  fires. Zero-copy dispatch over the existing handler machinery;
  `WaitForEvent[E]` / `WaitForEventContext[E]` wait for a (filterable)
  event with a timeout. Upstream only offers `AddHandler(interface{})`.
- **Builders for messages, embeds, components and modals**: fluent DSL
  (`NewEmbed`, `NewMessage`, `NewRow`, `NewPrimaryButton`/...,
  `NewSelectMenu`, `NewModal`, ...) that constructs the regular library
  structs — no manual struct literals, and `MessageBuilder.Send(s, ch)`
  posts directly. Upstream requires hand-assembling `MessageSend`,
  `MessageEmbed`, `ActionsRow`, `Button`, `SelectMenu` and modal payloads.
- **Interaction reply helpers**: one-line `Reply` / `ReplyEphemeral` /
  `ReplyEmbeds` / `ReplyComponents` / `ReplyDeferred(+Ephemeral)` /
  `FollowUp(+Ephemeral)` / `ReplyAutocomplete` / `ReplyModal` on top of
  `InteractionRespond` / `FollowupMessageCreate` with the usual
  `RequestOption` support.
- **HTTP interaction server**: `InteractionServer` is a standalone
  `http.Handler` that receives Discord interactions over HTTP (webhook
  mode) instead of the gateway — Ed25519 signature verification,
  automatic PING handling, per-type handlers, optional auto-defer with
  REST followups, replay protection and panic recovery.
- **Application command sync**: `ApplicationCommandsSync` diffs the
  desired command set against the registered one and issues only the
  minimal create/update/delete batch (dry-run and report included); JSON
  snapshots (`SaveApplicationCommandSnapshot`) enable offline drift
  checks in CI.
- **Voice audio toolkit**: paced opus/PCM senders (`SendOpusFrames`,
  `SendOpusStream`, `SendPCMStream`, `SendPCM16`), stdlib-only DCA
  parsing (`DCAReader`, `ReadAllDCA`, `ReadDCAFile`) and a 16-bit PCM
  helper set (resample, mix, fades, silence) over the public
  VoiceConnection API.
- **Typed handler middleware**: composable `TypedMiddleware[E]` chains
  (`ChainTyped`, `TypedPipeline`) with builtin panic recovery, timing
  logs, scope filters, deduplication (`DedupTyped`) and anti-flood
  throttling (`ThrottleTyped`), applied via `AddHandlerTypedWith`.
- **OAuth2**: full authorization-code flow (`OAuth2Flow`,
  `ExchangeCode`, `RefreshToken`), `TokenManager` with mutex-guarded
  refresh, jittered auto-refresh and persistence, the `OAuth2Transport`
  RoundTripper and 23 scope helpers with presets.
- **Generic TTL cache**: `TTLCache[K, V]` (TTL jitter, singleflight
  `GetOrLoad`, janitor, metrics, persistence) and `CachedSession` for
  Channel/Member lookups automatically invalidated by gateway events.

### Breaking changes vs upstream

- `Session.Dialer *websocket.Dialer` was replaced with
  `Session.DialTimeout time.Duration` (default 10s).
- `IntentsAllWithoutPrivileged` now includes the poll intents
  (`MESSAGE_POLL_VOTE_ADD/REMOVE` events). Build your intent set manually
  if you want the old default.
- The import path is `github.com/rxzsu/discordgo`.

## Performance tuning

The library exposes JSON hooks used by all encoding/decoding paths:

```go
discordgo.Marshal = json.Marshal   // default
discordgo.Unmarshal = json.Unmarshal
```

High-throughput bots can plug a faster implementation without forking.
Or simply build with a tag (the library ships opt-in accelerators):

```sh
go get github.com/goccy/go-json
go build -tags goccy ./...
```

Measured on this repo's benchmarks (Ryzen 5 5600X), gateway event decode:

| JSON          | small event (368 B) | large event (4.2 KB)             |
|---------------|--------------------:|---------------------------------:|
| encoding/json | 3679 ns/op          | 38203 ns/op (110 MB/s)           |
| goccy/go-json | 2877 ns/op (-22%)   | 15072 ns/op (**-61%**, 279 MB/s) |
| sonic         | 3486 ns/op (-5%)    | 17968 ns/op (-53%)               |

goccy wins on both small and large payloads; sonic's JIT advantage did
not materialize at these sizes. The `-tags goccy` / `-tags sonic` files
(`json_goccy.go` / `json_sonic.go`) set the hooks before any session is
created, or set `discordgo.Marshal`/`Unmarshal` manually **before**
calling `discordgo.New()`.

Comparisons on real API payloads (`json_bench_test.go`) confirm the same
picture on the marshal side: goccy is −21% decode / −66% marshal vs
stdlib, while sonic only wins on marshal. The default stays stdlib;
`-tags goccy` is the recommended accelerator for throughput-sensitive
deployments.

### Observing REST requests

Set `RequestObserver` before making requests to collect metrics without
adding a metrics backend dependency:

```go
s.RequestObserver = func(m discordgo.RESTRequestMetrics) {
    log.Printf("REST method=%s status=%d duration=%s retries=%d rate_limits=%d failed=%t",
        m.Method, m.StatusCode, m.Duration, m.Retries, m.RateLimits, m.Error != nil)
}
```

The callback runs once per completed REST operation, after releasing its
bucket, and includes bucket waits and automatic retries. It runs
synchronously and may be called concurrently; keep it short and protect
shared counters. The record describes the HTTP operation; high-level JSON
encoding and decoding are outside its scope. It has no URL, headers, or
payload fields.

For sharded bots, install the callback through
`ShardingConfig.ConfigureSession`.

### Event dispatch concurrency

By default every gateway event handler runs in its own goroutine, which
preserves handler isolation. For fine-grained control:

- `Session.SyncEvents = true` — dispatch synchronously (no goroutine
  per event); only for fully thread-safe handlers.
- `Session.DispatchWorkers = 64` (before `Open()`) — run handlers on a
  fixed worker pool of that size. Bounds memory during event storms
  (e.g. GUILD_CREATE on many servers); if the 1024-slot queue is full,
  jobs fall back to a goroutine so events are never dropped.

The rate limiter uses 16 independent shards (lock + map per shard), so
bucket lookups do not serialize REST traffic on a single mutex.

For maximum JSON throughput on amd64, `bytedance/sonic` can be plugged
into the same `Marshal`/`Unmarshal` hooks as goccy (see above).

## Getting Started

### Installing

This assumes you already have a working Go environment, if not please see
[this page](https://golang.org/doc/install) first.

```sh
go get github.com/rxzsu/discordgo
```

### Usage

Import the package into your project.

```go
import "github.com/rxzsu/discordgo"
```

Construct a new Discord client which can be used to access the variety of
Discord API functions and to set callback functions for Discord events.

```go
discord, err := discordgo.New("Bot " + "authentication token")
```

The fork additionally provides compile-time typed event handlers, fluent
builders and one-line interaction replies:

```go
// Typed handler: wrong event type = compile error, not a silent no-op.
discordgo.AddHandlerTyped(discord, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	// One-line ephemeral reply, then the slow work, then a followup.
	if err := s.ReplyEphemeral(i.Interaction, "Work started"); err != nil {
		return
	}
	// Buttons via the fluent component builders.
	row := discordgo.NewRow().
		AddButton(discordgo.NewPrimaryButton("Accept", "role-accept")).
		AddButton(discordgo.NewDangerButton("Deny", "role-deny")).
		Build()
	_ = s.ReplyComponents(i.Interaction, []discordgo.MessageComponent{&row})
	_, _ = s.FollowUpEphemeral(i.Interaction, "Done")
})

// Sending messages with embeds:
_, _ = discordgo.NewMessage().
	Content("Release!").
	AddEmbed(discordgo.NewEmbed().Title("v1.8.0").Color(0x5865F2).
		Field("Typed handlers", "compile-time safe", true)).
	Send(discord, channelID)
```

See Documentation and Examples below for more detailed information.

## Documentation

**NOTICE**: This library and the Discord API are unfinished. Because of that
there may be major changes to both in the future.

The DiscordGo project has its own documentation page, generated from the
source with [Godoc](https://godoc.org):

- [Godoc for this fork](https://pkg.go.dev/github.com/rxzsu/discordgo)
- [Upstream documentation site](https://bwmarrin.github.io/discordgo/)

The examples in [examples/](examples/) demonstrate the most common features,
including webhook-mode interactions (`examples/interaction_server`), the
fluent builders (`examples/builders`) and application command syncing
(`examples/commands_sync`).

## Roadmap to v1.0

The fork is currently at **v1.8.0**. The original v1.0 roadmap is retained
below as historical context:

- [x] Modern toolchain (Go 1.27.4) and maintained websocket dependency
- [x] Context-aware REST core (rate limit waits and retries respect
      cancellation); `WithContext`/`WithTimeout`/`WithQuery` are the
      official way to scope requests — the fork will **not** duplicate
      every REST method with a `*Context` variant
- [x] CI (build/vet/test -race on two Go versions, golangci-lint at
      0 issues)
- [x] Reproducible `go generate` for `eventhandlers.go` (verified
      byte-identical)
- [x] Deprecation audit: legacy APIs carry proper `// Deprecated:`
      godoc markers; dead aliases removed
- [ ] One release cycle (≈1 month) without breaking API changes
- [ ] Upstream sync check before final tag

### Stability promise (from v1.0.0 onward)

Semantic versioning: breaking API changes only in major versions,
deprecations get at least one minor release of overlap before removal.

## Keeping up with upstream

To pull in the latest upstream changes:

```sh
git remote add upstream https://github.com/bwmarrin/discordgo.git
git fetch upstream
git merge upstream/master
```

## Contributing

Issues and pull requests against this fork are welcome. Changes that also
apply to upstream are best sent to
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) first.

## License

See [LICENSE](LICENSE) (BSD-3-Clause, same as upstream).
