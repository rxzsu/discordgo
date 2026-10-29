# Changelog

All notable changes in this fork relative to upstream
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) are documented
here.

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
- Heartbeat reconnect threshold fixed: upstream computed it as
  `interval × 5ms` (~200ms for the standard 41s interval) instead of 5
  heartbeat intervals, causing spurious reconnects on slow ACKs.
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
