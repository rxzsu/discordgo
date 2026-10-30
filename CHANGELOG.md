# Changelog

All notable changes in this fork relative to upstream
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) are documented
here.

## v1.3.0

### Added
- Opt-in JSON accelerators via build tags: `go build -tags goccy` or
  `go build -tags sonic` wire the Marshal/Unmarshal hooks automatically.
  Measured: goccy cuts event decode by ~22% on small events and ~61% on
  large ones (38203 -> 15072 ns/op, 110 -> 279 MB/s); sonic measured
  slower than goccy at both sizes on this workload. Benchmarks
  extended with a large-payload dispatch case.

## v1.2.1

Bug-fix release, second bug-audit round (REST wrappers and new code).

### Fixed
- **P0** `NitroStickerPacks` requested a nonexistent endpoint
  (`/stickers/` — the by-ID route) and always 404'd; now uses
  `/sticker-packs` (the `EndpointNitroStickersPacks` constant also had
  a double-slash bug).
- `GuildPrune` sent an empty body (unexported struct field) — the
  `days` parameter was silently ignored and Discord always pruned with
  its 7-day default. Now serialized properly.
- `GuildTemplateCreate` had no error return: failures returned
  `(nil, nil)` and callers nil-dereferenced. Signature now includes
  `error` (technically breaking for that one method; required for
  correctness).
- ShardManager: handlers registered before `Open()` leaked onto shards
  connected later — the removal closure only detached from shards that
  existed at registration time. Handler entries now track per-session
  removers.
- Rate-limit bucket IDs for pinned messages / thread members /
  archived threads / webhook execute included the query string,
  fragmenting shared per-route limits into per-parameter buckets.
- `VerifyInteraction` closed the NopCloser body replacement instead of
  the original request body — leaking the server's body reader.
- `WithTimeout` leaked a context timer per request; the cancel now
  runs when the request completes.

## v1.2.0

Performance release, second optimization audit round. No API changes.

### Performance
- State: per-channel message index and per-guild presence index —
  `MessageAdd`/`Message`/`messageRemoveByID` and
  `presenceAdd`/`PresenceRemove`/`Presence` are O(1) instead of O(n)
  scans under the state lock (the hottest paths on busy servers;
  presences are the highest-volume event type on large guilds).
- Gateway: one persistent resettable zlib reader per session plus a
  reusable output buffer replaces a fresh 32KB-window reader and
  fresh buffer per compressed frame (compressed events measured at
  10 allocs vs ~16).
- Dispatch: the legacy raw-`*Event` dispatch pipeline is skipped
  entirely when no raw handlers are registered — roughly half the
  per-event dispatch work in the common case (`hasHandlers`).
- `Message.UnmarshalJSON` no longer allocates an empty Components
  slice for every message.
- State maps (`createMemberMap`, `onReady` guild map) are pre-sized —
  no ~17 rehash passes under the write lock for a 100k-member READY.

### Fixed
- `guild.MemberCount++/--` raced on concurrent member events
  (read-modify-write outside the state lock); now under the write lock.

## v1.1.1

Bug-fix release following a full-codebase agent audit. No API changes.

### Fixed
- voice: `Packet.Type` aliased the reused UDP read buffer — received
  packets' headers were corrupted by the next read before consumers
  saw them. Now copied. Also: `nonceCounter` is atomic (data race with
  reconnect), and `opusSender` reuses one buffer instead of 3
  allocations per audio packet.
- ShardManager: `remove()` panicked on any closure handler (`comparing
  uncomparable type func`); handlers are now tracked by pointer
  identity. `ShardForGuildID` takes the manager lock. `Open` no longer
  connects remaining shards if `Close` ran during the identify delay.
- event: `onceHandlers` were mutated under a shared RLock — concurrent
  same-type events could fatal (`concurrent map writes`) and
  double-fire once handlers. Drain is now exclusive (`onceMu`);
  `dispatchJobs` reads synchronized.
- state: `ThreadMembersUpdate` panicked with 2+ removed members and
  compared the wrong field (`ThreadMember.ID` instead of `UserID`);
  `ThreadMemberUpdate` mutated shared state without the write lock;
  `GuildRemove` now cleans `channelMap`/`memberMap`; `onReady` resets
  indexes so entities of a previous session don't linger after
  non-resumable reconnects.
- wsapi: handshake reads in `Open` are bounded (30s) — a stalled
  gateway no longer holds the session lock forever; gateway writers no
  longer hold `s.RLock` across socket writes.

Found by a two-agent code audit (bug-focused and performance-focused)
with manual verification of every claim.

## v1.1.0

### Added
- `Session.DispatchWorkers` — optional fixed worker pool for event
  handlers. Bounded memory during event storms; 1024-slot queue with
  goroutine fallback (events are never dropped). Set before `Open()`.
- Sonic as a drop-in alternative to goccy for the JSON hooks
  (documented in README).

### Changed
- `RateLimiter` now uses 16 independent shards (per-shard lock + map)
  instead of a single mutex over one map, so bucket lookups do not
  serialize concurrent REST traffic. Technically breaking for code that
  relied on the previously embedded (unintended) `sync.Mutex` methods
  on `RateLimiter` itself — use the documented API instead.
- New rate limiter introspection: `GetBucketIfPresent`, `BucketCount`.

## v1.0.0

First stable release of the fork.

### Validation
- Live smoke test against the real Discord API: gateway connect (READY),
  REST round-trips (message send/reply), message content intent, DM
  channels, heartbeat latency ~155-175ms, and a 10+ minute soak with
  zero reconnects and zero heartbeat errors.

### Stability promise (now in effect)
- Semantic versioning: breaking API changes only in major versions.
- Deprecated APIs get at least one minor release of overlap before
  removal.

## v1.0.0-rc.6

### Fixed
- Reverted the heartbeat-ACK threshold change introduced in v0.30.0.
  A live smoke test caught it: the threshold collapsed to ~200
  microseconds, causing a reconnect after the first heartbeat. The
  upstream constant was correct all along; docs corrected accordingly
  (WHY-FORK.md bug table updated).

## v1.0.0-rc.5

### Performance
- Gateway event decoding: `onEvent` now uses `Unmarshal` on the message
  buffer directly instead of constructing a `json.Decoder` per event —
  measured dispatch cost dropped from 3760ns/15 allocs to
  3171ns/8 allocs per event (~16% faster, ~7 fewer allocations).
- README: documented the `Marshal`/`Unmarshal` hooks for plugging a
  faster JSON implementation (e.g. goccy/go-json) without forking.
- Benchmarks for the event dispatch path included.

## v1.0.0-rc.4

### Fixed
- Memory: the default state no longer keeps every message forever.
  `NewState()` did not set `MaxMessageCount`, so the per-channel cache
  grew without bound on busy servers; it now defaults to 1000 messages
  per channel (override via `State.MaxMessageCount`, set a huge value
  to restore the old behavior).

### Performance
- Rate limit bucket IDs are computed without allocation
  (`BucketIDFromURL`): 4.9ns/0 allocs vs 26.6ns/1 alloc for the old
  `strings.SplitN` approach, on every REST request.
- Gateway and voice event paths no longer copy the raw payload into a
  string for debug logging unless debug logging is enabled (previously
  every received event paid a full payload-sized allocation).
- Benchmarks added (`bench_test.go`): bucket ID computation, full REST
  request path, event dispatch, state message add.

## v1.0.0-rc.3

### Added
- Full REST coverage of APIs whose endpoints existed but had no methods
  (found by an automated endpoint-coverage audit):
  - Stickers: `NitroStickerPacks`, `GuildStickers`, `GuildSticker`,
    `GuildStickerCreate` (multipart upload), `GuildStickerEdit`,
    `GuildStickerDelete`.
  - Guild widget: `GuildWidget`, `GuildWidgetEdit`.
  - Soundboard: `GuildSoundboardSounds`, `GuildSoundboardSound`,
    `GuildSoundboardSoundCreate` (multipart upload),
    `GuildSoundboardSoundEdit`, `GuildSoundboardSoundDelete`,
    `ChannelSoundboardSoundSend`, plus the
    `GuildSoundboardSoundCreate/Update/Delete` gateway events.

### Changed
- Voice: heartbeat ACKs are now tracked; new
  `VoiceConnection.VoiceHeartbeatLatency()` (previously the OP3 response
  handler was an empty TODO).

## v1.0.0-rc.2

### Added
- `ShardManager` (`sharding.go`): manages one Session per gateway shard
  with shared event handlers. Auto-sizes the shard count and identify
  delay from the GatewayBot endpoint (respects `max_concurrency`),
  `ShardForGuildID`/`ShardIDForGuild` helpers, and an example
  (`examples/sharding`). Upstream has no sharding support.
- REST method `GuildBulkBan` (`POST /guilds/{id}/bulk-ban`) with
  `GuildBulkBanResponse` — ban up to 200 users in one request.

## v1.0.0-rc.1

### Added
- v1 roadmap and stability promise in README.

### Changed
- `VERSION` now reflects fork versioning (`1.0.0-rc.1`, was upstream's
  `0.29.0`).
- Deprecation audit: `UserAvatar`, `GuildMemberEditComplex`,
  `ChannelEditComplex`, `ChannelFileSendWithMessage`,
  `RequestGuildMembersBatch(List)`, `IntentsGuildBans`,
  `MessageFlagsSupressEmbeds` now carry proper `// Deprecated:` godoc
  markers. Internal call sites no longer route through deprecated APIs.
- `go generate` output for `eventhandlers.go` verified byte-identical
  with the committed file.

### Removed
- Dead `EndpointOauth2*` aliases (unused, marked "remove in the next
  release" upstream).

## v0.30.1

### Fixed
- RTP timestamp overflow handling in the voice `opusSender` (the old
  wrap check was dead code; long voice sessions could corrupt the RTP
  timestamp after ~13 hours).
- Full golangci-lint pass (0 issues): errcheck findings, deprecated
  `io/ioutil`, ineffectual assignments, style modernization.
- CI green (Go 1.26/1.27 matrix, `go test -race`, golangci-lint).

## v0.30.0

### Added
- Go 1.27 toolchain (`go.mod` was still on 1.13).
- `RESTError.Code()` — typed access to the Discord API error code of a
  failed request.
- Request options `WithTimeout(d)` and `WithQuery(key, value)`.
- REST methods `ChannelPollAnswerVoters` and `ChannelPollExpire` (poll
  endpoints existed, the wrappers did not).
- REST method `EntitlementDelete`.
- Rate limiter: `LockBucketContext` / `LockBucketObjectContext` —
  context-aware waiting for a rate limit bucket; `RemoveBucket`;
  automatic pruning of stale buckets once the map exceeds 512 entries.
- `Session.DialTimeout` — dial timeout for gateway and voice websocket
  connections (default 10s).
- Unit tests for all of the above (`fork_test.go`), including httptest
  coverage of context cancellation during rate limit retries.

### Changed
- Websocket layer migrated from `gorilla/websocket` (unmaintained) to
  `github.com/coder/websocket` v1.8.15. `Session.Dialer` was replaced by
  `Session.DialTimeout` (breaking).
- Heartbeat reconnect threshold: initially changed to a plain interval
  count; REVERTED in v1.0.0-rc.6 — upstream's `5 * time.Millisecond`
  was correct given that `HeartbeatInterval` decodes milliseconds into
  a nanosecond-based time.Duration. No behavioral change vs upstream.
- REST retries on 429/5xx now respect the request context: a cancelled or
  timed-out request aborts instead of sleeping out `retry_after`.
- `IntentsAllWithoutPrivileged` now includes
  `IntentGuildMessagePolls`/`IntentDirectMessagePolls` (breaking for bots
  that rely on the default intent set).
- Voice reconnect loop now stops when the `VoiceConnection` was removed
  from the session (manual `Disconnect()`) instead of retrying forever.
- Module path renamed to `github.com/rxzsu/discordgo`; UserAgent updated
  accordingly.
- Modernized `.golangci.yml`; `io/ioutil` usage removed.

## Upstream baseline

Fork point: upstream `master` at commit `f43dd94` (2025).
