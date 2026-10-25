# Changelog

All notable changes in this fork relative to upstream
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) are documented
here.

## Unreleased

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
