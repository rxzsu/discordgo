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

- **Go 1.27+** required; `github.com/coder/websocket` instead of the
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
- **`GuildBulkBan`**: ban up to 200 users in a single API call.
- **Stickers, guild widget, soundboard**: full REST coverage of APIs
  that upstream defined endpoints for but never implemented methods for.

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

High-throughput bots can plug a faster implementation without forking:

```go
import "github.com/goccy/go-json"
discordgo.Marshal = goccy.Marshal
discordgo.Unmarshal = goccy.Unmarshal
```

Set these **before** calling `discordgo.New()`.

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

See Documentation and Examples below for more detailed information.

## Documentation

**NOTICE**: This library and the Discord API are unfinished. Because of that
there may be major changes to both in the future.

The DiscordGo project has its own documentation page, generated from the
source with [Godoc](https://godoc.org):

- [Godoc for this fork](https://pkg.go.dev/github.com/rxzsu/discordgo)
- [Upstream documentation site](https://bwmarrin.github.io/discordgo/)

The examples in [examples/](examples/) demonstrate the most common features.

## Roadmap to v1.0

The fork is currently at **v1.0.0-rc.1**. Final v1.0.0 ships once the
following are complete:

- [x] Modern toolchain (Go 1.27) and maintained websocket dependency
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
