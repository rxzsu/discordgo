# Why this fork?

`rxzsu/discordgo` is an actively maintained fork of
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) — the most
popular Go library for the Discord API. Upstream is great but has been
accumulating technical debt: a frozen toolchain, an unmaintained
websocket dependency, and a handful of genuine bugs. This fork fixes
those and adds capabilities upstream lacks.

## Real bugs fixed

| Bug | Upstream behavior | Fork behavior |
|---|---|---|
| State memory leak | Default state kept **every received message forever** (`MaxMessageCount` defaulted to 0 = unlimited) — gigabytes of RAM on busy bots | Defaults to 1000 messages per channel |
| Voice RTP timestamp overflow | The wrap-around check was dead code (`uint32 >= 0xFFFFFFFF` unreachable); long voice sessions could corrupt the RTP timestamp after ~13h | Correct overflow detection |
| Voice reconnect loop | Retried forever even after the user called `Disconnect()` | Stops when the connection is no longer registered |

## Features upstream doesn't have

- **ShardManager** — multi-shard gateway support with shared handlers and
  automatic shard-count/identify-delay discovery. Upstream has no
  sharding support at all.
- **Context-aware REST** — rate limit waits and 5xx retries abort as soon
  as the request context is cancelled; `WithContext`/`WithTimeout`/
  `WithQuery` options on every REST method.
- **REST APIs upstream defined endpoints for but never implemented**:
  Stickers, Guild Widget, Soundboard, `GuildBulkBan`, Poll API methods.
- **Typed errors** — `RESTError.Code()` for Discord API error codes
  (e.g. 50013 Missing Permissions) without string parsing.
- **Rate limiter hygiene** — context-aware bucket locking, stale-bucket
  pruning, `RemoveBucket`.
- **Observability** — `VoiceHeartbeatLatency()` in addition to the
  gateway one; `RESTError` and `RateLimitError` carry full context.

## Measured performance (benchmarks in `bench_test.go`)

- Rate limit bucket key: **4.9ns / 0 allocs** vs upstream's 26.6ns / 1
  alloc per REST request (5.4× faster, allocation-free).
- Gateway event dispatch: **3171ns / 8 allocs** vs upstream's
  3760ns / 15 allocs (~16% faster, ~47% fewer allocations per event).
- Every gateway event in upstream copies the raw payload into a string
  for debug logging *before* checking the log level; the fork does not.
- Plug in `goccy/go-json` or another fast JSON via the `Marshal`/`Unmarshal`
  hooks for a further ~30–40% on dispatch (documented in README).

## Modern foundation

- Go 1.27, CI: build + vet + `go test -race` on Go 1.26/1.27,
  golangci-lint at **0 issues**, CodeQL.
- `github.com/coder/websocket` instead of the unmaintained
  `gorilla/websocket`.
- Dependency count: **1**.
- Every release CI-verified and tagged; deprecations carry proper
  godoc markers; SemVer promised from v1.0.0.

## Staying compatible

The fork tracks upstream: no gratuitous API breaks, only two (both
documented in README) since the fork point, and upstream commits are
merged in regularly.
