# Changelog

All notable changes in this fork relative to upstream
[bwmarrin/discordgo](https://github.com/bwmarrin/discordgo) are documented
here.

## v1.8.0

Large feature release: a webhook-mode interaction server, application
command sync, typed event handlers and middleware, fluent builders,
voice audio utilities, a full OAuth2 client flow and a generic TTL cache,
plus performance work on the REST, gateway and rate-limit paths.

### Added (HTTP interaction server & command sync)

- HTTP interaction server (webhook interactions mode): new `InteractionServer`
  type is a standalone `http.Handler` that receives Discord interactions over
  HTTP instead of the gateway — Ed25519 signature verification (`X-Signature-Ed25519`
  / `X-Signature-Timestamp`, stdlib `crypto/ed25519`, no new dependencies),
  automatic PING→PONG handshake, per-type handlers (`OnApplicationCommand`,
  `OnComponent`, `OnAutocomplete`, `OnModalSubmit`, `OnFallback`) that return the
  interaction response, JSON and multipart (file) responses, optional
  `AutoDefer` for deferred ACK + REST followups, body size limit, signature
  timestamp freshness (replay protection) and handler panic recovery
  (`NewInteractionServer`, `InteractionPublicKeyFromHex`).
- Application command sync: `Session.ApplicationCommandsSync` compares the
  desired command set with the registered one by normalized content hash
  (`ApplicationCommandContentHash`, `ApplicationCommandsEqual`) and issues only
  the minimal diff — POST per new, PATCH per changed, DELETE per removed
  (opt-in `DeleteRemoved`), never re-sending unchanged commands, plus a
  `DryRun` mode and full `ApplicationCommandSyncReport`/plan
  (`CompareApplicationCommands`). Normalization filters server-assigned fields
  and API default values so equal content never triggers phantom updates.
- Local application-command snapshots for offline drift detection (e.g. CI):
  `Session.ApplicationCommandSnapshot`, `SaveApplicationCommandSnapshot` /
  `LoadApplicationCommandSnapshot` (JSON) and
  `ApplicationCommandSnapshot.DiffCommands` diff a desired command set against
  a saved snapshot with zero API traffic.

### Added (ergonomics)

New capability layer, entirely additive and implemented without touching
any existing file (typed handlers ride on the existing
`AddHandler`/dispatch machinery; builders and reply helpers construct the
regular library structs). None of these APIs exist upstream.

- **Typed event handlers via generics** (`typed.go`): `AddHandlerTyped(s,
  func(*Session, *MessageCreate) {...})` and `AddHandlerOnceTyped` carry
  the event type as a compile-time generic parameter, so a handler
  registered for the wrong event type can no longer compile — upstream's
  `AddHandler(interface{})` only logs "invalid handler type" at runtime and
  the handler silently never fires. Implementation is zero-copy: the
  generic wrapper implements the internal `EventHandler` interface and
  dispatch keeps the single type assertion of the generated handlers, with
  full compatibility with the returned remover, once-handlers,
  `SyncEvents`, `DispatchWorkers` and the raw-dispatch fast path. Event
  types are resolved by probing the existing handler registry, so every
  event supported by `AddHandler` — WSAPI and synthetic (`Connect`,
  `Disconnect`, `RateLimit`, ...) — works, including future additions.
  Bonus helpers: `WaitForEvent[E](s, timeout, filter)` /
  `WaitForEventContext[E](ctx, s, filter)` block for the next (optionally
  filtered) event and always clean up their internal handler.
- **Builders for messages, embeds and components** (`builders.go`,
  `builders_components.go`): fluent DSL over the raw structs — `NewEmbed`
  (title/description/color/timestamp/author/footer/image/thumbnail/
  fields), `NewMessage` (content, embeds, rows, files, stickers, poll,
  reply reference, `Send(s, channelID)`), `NewRow` +
  `NewButton`/`NewPrimaryButton`/`NewSecondaryButton`/`NewSuccessButton`/
  `NewDangerButton`/`NewLinkButton`, `NewSelectMenu` plus the
  auto-populated `NewUserSelectMenu`/`NewRoleSelectMenu`/
  `NewChannelSelectMenu`/`NewMentionableSelectMenu`, and modals via
  `NewModal` + `NewLabel` + `NewTextInput`. Everything `Build()`s into the
  existing `MessageSend`/`MessageEmbed`/`ActionsRow`/`Button`/`SelectMenu`/
  `InteractionResponse` structs — no new wire types, no kernel changes.
  `Build()` snapshots builder state, so builders stay reusable.
- **Interaction reply helpers** (`interaction_reply.go`): one-line
  responses on top of `InteractionRespond`/`FollowupMessageCreate` with
  `RequestOption` passthrough — `Reply`, `ReplyEphemeral`, `ReplyEmbeds`,
  `ReplyEmbedsEphemeral`, `ReplyComponents`, `ReplyDeferred`,
  `ReplyDeferredEphemeral`, `FollowUp`, `FollowUpEphemeral`, `FollowUpData`
  (all wait=true), `ReplyAutocomplete` (rejects an empty choice list with
  the new `ErrNoAutocompleteChoices` instead of sending a payload Discord
  rejects, since the response struct omits empty choices) and
  `ReplyModal` (consumes the `ModalBuilder` directly).

### Added (voice utilities)

- Paced opus/PCM streaming helpers over the public VoiceConnection API:
  `SendOpusFrames`, `SendOpusStream`, `SendPCMStream` and `SendPCM16` with
  a user-supplied `OpusEncodeFunc` — correct 20 ms frame pacing, trailing
  silence flush (`OpusSilenceFrame`, 5 frames = 100 ms) and graceful stop
  with draining (`StopVoice`, `WithStopSignal`, `WithGracefulStop`). No
  new dependencies.
- DCA stream support, stdlib only: `DCAReader` (auto-detects the `dca0`,
  JSON-header and raw layouts), `ReadAllDCA` and `ReadDCAFile` — parse
  DCA files into opus frames and play them with the senders above.
- Pure PCM toolkit for 16-bit mono 48 kHz audio: `ChunkPCM16`, `PadPCM16`,
  `StereoToMono16`, `MonoToStereo16`, `ResampleLinear16`, `FadeIn16`,
  `FadeOut16`, `SilencePCM16`, `MixPCM16`, plus `WaitVoiceReady` and the
  voice audio constants.

### Added (typed handler middleware)

- Composable middleware for typed event handlers built on
  `AddHandlerTyped`: `TypedMiddleware[E]`, `UseTyped`, `ChainTyped`
  (early-exit chains), `AddHandlerTypedWith` / `AddHandlerOnceTypedWith`
  and a reusable `TypedPipeline[E]`.
- Builtin middlewares: panic recovery (`RecoverTyped`), handler timing
  logs (`LogTyped`), scope filtering (`ScopeFilter` +
  `FilterMessageCreateEvents`, `FilterInteractionEvents`, generic
  `FilterTyped`/`FilterEq`/`FilterOneOf`/`FilterNot`), event
  deduplication (`DedupTyped`) and per-key anti-flood token buckets
  (`ThrottleTyped`).

### Added (OAuth2)

- Full OAuth2 authorization-code flow: `OAuth2Flow.AuthCodeURL` /
  `AuthCodeURLWithParams`, `ExchangeCode`, `RefreshToken` and the
  structured `OAuth2Error` (oauth2flow_token.go).
- `TokenManager`: lazy and forced refresh under a mutex (one refresh
  shared by N concurrent callers), a background auto-refresh goroutine
  with jitter, persistence via `SaveJSON`/`LoadJSON`/`SaveFile`/`LoadFile`
  and an `OnTokenChange` hook (oauth2flow_client.go).
- `OAuth2Transport`, a RoundTripper that injects the Bearer token and
  restarts a request after a 401; attach it via
  `TokenManager.AttachSession` / `.HTTPClient` (oauth2flow_client.go).
- Scope helpers (23 `OAuth2Scope` constants, `JoinOAuth2Scopes` /
  `ParseOAuth2Scopes` / `HasOAuth2Scope` and the `OAuth2Scopes*` presets)
  and Session methods `CurrentApplicationInfo`, `OAuth2CurrentUser`,
  `OAuth2CurrentUserGuilds`, `OAuth2CurrentUserConnections`,
  `OAuth2CurrentUserGuildJoin`, plus the `WithBearerToken` request option
  (oauth2flow_scopes.go).

### Added (TTL cache)

- Generic `TTLCache[K, V]`: TTL with jitter, singleflight `GetOrLoad`,
  hit/miss metrics, an optional janitor goroutine with `Close`,
  `MaxEntries`, an `OnEvict` hook and JSON persistence preserving the
  remaining TTL (ttlcache.go).
- `CachedSession` — TTL-cached `Channel` / `GuildMember` lookups with
  deduplication of concurrent requests and automatic invalidation on
  gateway events (ChannelUpdate / ChannelDelete / GuildMemberUpdate /
  GuildMemberRemove / GuildDelete) (ttlcache_state.go).

### Added (examples)

- `examples/interaction_server` — webhook-mode interaction bot:
  `NewInteractionServer` + `InteractionPublicKeyFromHex`,
  `OnApplicationCommand` / `OnComponent`, inline `*InteractionResponse`
  returns vs REST `ReplyEphemeral` (204), plain `net/http` on `:8080`
  (endpoint setup documented in the file header).
- `examples/builders` — fluent builders: `NewMessage` / `NewEmbed` /
  `NewRow` with colored buttons and a multi-select menu,
  `AddHandlerTyped` gateway handling, `ReplyComponents` / `ReplyEphemeral`
  / `FollowUpEphemeral`.
- `examples/commands_sync` — `ApplicationCommandsSync` with commands
  declared in code, `-dry-run` plan-only mode, `-delete-removed` and a
  printed sync report.

### Fixed

- `State.UserColor` / `State.MessageColor` no longer sort the shared
  `guild.Roles` slice in place (a data race with concurrent state writers
  such as `RoleAdd`); they sort a copy instead.

### Changed (performance)

- REST: −20% allocations per request — precomputed canonical header keys
  instead of `Header.Set` (whose re-canonicalization scan was paid on
  every request for non-canonical keys like `authorization`), and an empty
  request body is passed as nil without the `bytes.Buffer`/`GetBody`
  wrapper.
- Gateway: one allocation fewer per compressed frame — reusable adler32/
  flate state and stack buffers replace `zlib.Reset`, which allocated a
  fresh adler32 digest for every binary frame.
- Rate limit: allocation-free reading of `Retry-After`/`Date` (−9% on the
  release path).
- Measured JSON backend comparison on real payloads: goccy is −21% decode
  / −66% marshal vs stdlib; sonic only wins on marshal. The default stays
  stdlib; the `goccy` build tag is recommended for throughput-sensitive
  deployments.
- New benchmarks for the gateway write path and the JSON hooks
  (`wsapi_bench_test.go`, `json_bench_test.go`).

### Compatibility notes

- The module now requires **Go 1.27.4+** (`go 1.27.4` in `go.mod`);
  CI already tested on the latest 1.27.x patch release.
- `goccy/go-json` and `bytedance/sonic` are now recorded as direct module
  requirements (their build-tag files import them; previously listed as
  indirect). They remain opt-in: builds without `-tags goccy` / `-tags sonic`
  do not link either package.

### Tests

- `voiceutils_test.go` covers the voice streaming helpers (frame pacing,
  silence flush, graceful stop), DCA layout sniffing and the PCM toolkit;
  `middleware_test.go` covers the typed handler middleware chain
  (recovery, logging, filters, dedup, throttling);
  `oauth2flow_test.go` covers the authorization-code flow, token
  refresh/persistence and the transport; `ttlcache_test.go` covers the
  TTL cache expiry/singleflight/janitor/eviction and the cached session
  invalidation.
- `webhookserver_interactionserver_test.go` and `appcmdsync_test.go`
  cover the interaction server (signature/handshake/defer behaviour) and
  the command sync/snapshot features above.
- `typed_test.go`: dispatch through the real `handleEvent` path for
  WSAPI and synthetic events (table-driven), zero-copy instance-identity
  check, typed+untyped handlers side by side, once/remove semantics,
  invalid event type guard, `WaitForEvent` filtering/timeout/cancel and
  handler-leak check.
- `builders_test.go`: exact wire-JSON comparison (via the library's
  replaceable `Marshal`) for embeds, button rows, select menus and modals,
  including the auto-populated menu variants; `MessageSend` equivalence
  with hand-written struct literals; `Send()` verified against an
  httptest server; `Build()` snapshot isolation.
- `interaction_reply_test.go`: every helper verified against an httptest
  server — request method, path, query (`wait=true`) and decoded JSON body
  (response types 4/5/8/9, ephemeral flags, components, modal payload),
  plus the empty-autocomplete guard.

## v1.7.0

### Fixed
- Five more voice data races and a nil panic, continuing the
  snapshot-under-lock audit from v1.6.0: the OP5 handler read the
  speaking-update handler slice without the voice lock while
  `AddHandler` appends under it; the OP2 READY handler wrote `v.op2`
  unlocked while `udpOpen`/`opusSender` read it (a repeated OP2, e.g. a
  voice-server change, raced a live sender); the public `OpusSend` /
  `OpusRecv` channels were assigned unlocked and the `udpConn` snapshot
  straddled two lock sections (TOCTOU against `udpOpen`); `opusSender`
  read `v.op2.SSRC` unlocked in the hot loop; and `Disconnect` read
  `v.GuildID` twice unlocked, so a voice-server change could delete the
  wrong map entry. Plus `MultipartBodyWithJSON` now returns an error
  instead of panicking on a nil file / nil `Reader`.
- gofmt fix in the `GuildIncidentActionsEdit` doc comment (this was the
  cause of the CI lint failure on the v1.6.0 push).

### Changed (performance)
- Gateway dispatch path (`onEvent`): the fixed envelope fields (op/s/t/d)
  are extracted by a strict single-pass scan instead of a full JSON decode,
  removing the second full parse of the payload and the `RawData` copy that
  preceded the typed decode. Anything unexpected falls back to the general
  decoder (goccy/sonic builds included), so acceptance is never looser than
  `Unmarshal`; `RawData` now aliases the reused read buffer and every
  in-library consumer is synchronous (raw `__EVENT__` handlers still get
  their own copy). Measured with benchstat (n=6): dispatch −20% time,
  compressed frames −19% time / −9% bytes / 10→9 allocs, 4 KiB events
  −17% time / −47% bytes.
- Voice-state lookups in `State` are indexed: `VoiceState()` was a linear
  scan over `Guild.VoiceStates` (run without the state read lock — also a
  data race fixed here) and is now an O(1) map lookup, maintained by
  `GuildAdd` / `voiceStateUpdate` / `GuildRemove` / `onReady` alongside the
  existing message and presence indexes.
- Hot `State` paths take one lock instead of two: `MessageAdd`,
  `messageRemoveByID`, `Message` and `voiceStateUpdate` used to acquire a
  read lock via `Channel()`/`Guild()` before re-acquiring the write lock.
- New benchmarks: `BenchmarkStateVoiceStateLookup` (10k voice states),
  `BenchmarkStateMessageCreatePath`; envelope tests in `fork_test.go`
  cross-check the fast scan against the JSON decoder over truncations and
  byte mutations.

## v1.6.0

### Fixed (voice)
- Seven data races / nil panics in the voice connection, the same
  snapshot-under-lock bug class fixed in v1.4.x, now closed out for
  voice: `ChangeChannel` wrote `ChannelID`/`deaf`/`mute`/`speaking`
  without the voice lock; `ChangeChannel`/`Disconnect`/`reconnect`
  wrote directly to the session gateway without the session lock or a
  nil check (nil panic whenever the gateway was closed or reconnecting
  — a `writeSessionGateway` helper now snapshots under the session
  read lock, mirroring `requestGuildMembers`); `opusReceiver` read
  `v.aead` unlocked (the mirror of the `opusSender` fix from v1.4.0);
  `reconnect` read session and voice state without locks, with a safe
  documented lock order (`v.Lock` → `session.RLock`); `open`/`wsListen`
  (op 4014) published and cleared `wsConn` without `wsMutex`; and the
  OP2 handler started heartbeat/opus goroutines from unlocked reads.
  Four regression tests in `voice_audit_test.go` reproduce data races
  and a nil panic against the pre-fix code and pass cleanly after.

### Changed (performance)
- REST request path: the `http.Request` clone is no longer made on the
  first attempt (the most common case) but only on retries — a 429
  retry kept an unchanged sequence and silently dropped the body,
  now covered by `Test429RetryPreservesBody`; `req.URL.String()` is
  computed lazily off the hot path; response bodies are read into a
  single buffer sized by `Content-Length` (capped at 16 MiB) instead
  of repeatedly growing from 512 bytes. Measured with benchstat
  (n=5): plain request path −32% time / −30% bytes / 30→20 allocs,
  requests with a body −51% time / −47% bytes.
- Rate-limit header reads are allocation-free: Discord sends
  `X-RateLimit-*` headers in mixed case, so each `Header.Get` paid a
  canonicalization allocation; precomputed canonical keys remove all
  four per-response allocations (−34% time on the bucket-release path,
  96→0 B/op). The v1.5.0 bucket identity / shared 429 cooldown
  semantics are untouched.

### Added
- Guild Incident Actions API (raid panic mode):
  `GuildIncidentActionsEdit` sets or lifts temporary invite and
  direct-message restrictions, `GuildWithIncidentsData` fetches a guild
  with its current `IncidentsData`, and the `IncidentsData` /
  `GuildIncidentActionsParams` types (zero-time fields are sent as
  explicit `null`s to clear a restriction).
- `GuildAuditLogsComplex` with the full audit log filter set: the existing
  `GuildAuditLog` signature is unchanged and additionally supports the
  `after` and `application_id` filters via `GuildAuditLogFilter`.
- Guild Member Verification API: `GuildMemberVerification` and
  `GuildMemberVerificationEdit` read and update the member verification
  form of a community guild.

## v1.5.0

### Added
- Optional `Session.RequestObserver` with one `RESTRequestMetrics` record
  per completed REST operation: duration, latest HTTP status, attempts,
  retries, rate-limit responses, and final error.
- `Session.OpenContext` and `ShardManager.OpenContext` for cancellable
  gateway discovery, dialing, handshakes and shard startup delays.
- `ShardingConfig.ConfigureSession` to configure each shard and the
  gateway discovery session before connecting.

### Fixed
- Shard lookup uses the total shard count during partial startup and returns
  nil for shards not connected yet.
- Disconnect callbacks can call manager methods without deadlocking shutdown.
- Rate-limit cache pruning preserves bucket identity while callers hold
  references; active cooldowns survive direct reuse and garbage collection.
  `RemoveBucket` now retains locked buckets and active windows.
- Global and local cooldowns use the longer delay and are rechecked after
  waiting. Concurrent responses cannot shorten a global cooldown.
- HTTP 429 bodies update shared local/global limits before releasing the
  bucket, including when automatic retries are disabled. `Retry-After`
  headers and sub-millisecond durations are honored; invalid durations
  are rejected.
- REST request timeouts now include bucket waits and all retries.
  Context cancellation also interrupts waiting for a busy bucket mutex.
- Dispatch workers retain their own channels and exit after shutdown.
- Snowflake parsing rejects uint64 overflow instead of wrapping to zero.
- Shard manager startup no longer holds its mutex during network calls;
  `Close` cancels an in-progress startup and concurrent opens are rejected.
- Raw bot tokens are prefixed with `Bot ` by the shard manager, matching
  its documented configuration contract.
- Heartbeat workers stop when their connection has already been closed,
  avoiding a reconnect caused by a delayed heartbeat after shutdown.

### Changed
- Guild-to-shard lookups no longer allocate or copy the session slice.

### Compatibility notes
- Existing `Open()` methods remain available. Closing a manager during
  startup now causes the pending open call to return `context.Canceled`.
- Request options are applied once per operation rather than reapplied
  for every automatic retry, preserving the original timeout deadline.
- `RemoveBucket` is now safe cache eviction; it no longer forcibly resets
  an active endpoint's rate limit.

## v1.4.1

Bug-fix release: final events/dispatch audit round. No API changes.

### Fixed
- **HIGH** deadlock: op 7 (Reconnect) / op 9 (Invalid Session) received
  during the `Open` handshake called `CloseWithCode`/`reconnect`, which
  need the session lock already held by `Open` itself — one failed
  RESUME bricked the session permanently (Open never returned, Close
  blocked). The handshake now tears the connection down and returns a
  retryable `ErrWSNotReady` instead.
- Deadlock: `handle()` re-acquired the handlers read-lock already held
  by its caller — a concurrent `AddHandler*` arriving in the tiny
  window between the two acquisitions blocked both forever. The nested
  acquisition is gone (the once-list drain keeps its own `onceMu` with
  a consistent lock order).
- `Disconnect` fired for sessions that were never connected (e.g. a
  shard whose `Open` failed during ShardManager startup) or twice for
  one real loss; guarded by a captured connected flag.
- op 1 heartbeat reply and `ChannelVoiceJoinManual` wrote `wsConn`
  without a snapshot — panic race against `Close` on the gateway hot
  path; both now snapshot under the lock like every other writer.

## v1.4.0

Fourth audit round (voice deep-dive + interactions/components/webhook/misc sweep), plus gateway-event fixes found by hand.

### Fixed (voice)
- `opusSender` called `Seal` on a nil AEAD if audio was pushed as soon as `Ready` flipped (OP4 had not arrived) — nil-interface panic killed the whole process; now snapshots the AEAD under the lock and drops frames until OP4 arrives.
- UDP IP-discovery read had no deadline: a dropped response hung `udpOpen` forever holding the voice lock, bricking `Close`/`Disconnect`/`Speaking`. Now bounded by a 10s deadline.
- Encryption mode was hardcoded: OP2's offered `modes` and OP4's granted `mode` were never checked — a different negotiated mode meant Discord silently dropped every audio packet. The mode is now validated and a mismatch logs loudly instead of failing silently.
- `Speaking()` dereferenced `wsConn` between an unlocked nil-check and the write (panic vs reconnect/Close); now snapshotted under the write mutex.
- RTP timestamp reset to 0 on wrap instead of using the naturally wrapped value — a backward timestamp jump every ~24.9h for continuous senders. Now wraps naturally.
- `ChannelVoiceJoin` TOCTOU could create two live connections for one guild; lookup+insert are now under one write lock.
- Voice heartbeat `LastVoiceHeartbeatSent` was written and read under different locks (torn time.Time reads).

### Fixed (events)
- **Stage instance events never fired**: the library registered `STAGE_INSTANCE_EVENT_*` (no such Discord events); the real `STAGE_INSTANCE_CREATE/UPDATE/DELETE` are now registered. The old struct names remain as deprecated type aliases, so existing code compiles.
- New events: `GUILD_INCIDENTS_UPDATE`, `VOICE_CHANNEL_STATUS_UPDATE`.

### Fixed (misc)
- `MessageAllowedMentions` zero value emitted `"parse":null` — which means "use app defaults" (everything pings) — while its documentation implied "suppress all mentions". A custom marshaler now emits `"parse":[]` for the zero value; user-input mention suppression works as documented.
- `MessageEdit.ID/.Channel` leaked into every edit PATCH body as junk JSON fields; now tagged out.
- Multipart file names with CR/LF could inject header lines (header injection); stripped.
- `ApplicationCommandOption.MaxValue` could not serialize the valid bound 0; now a pointer.
- `Member.AvatarURL` panicked on partial members with a nil `User`; guarded.
- Added `WebhookTypeApplication` (3), `InteractionResponseLaunchActivity` (10), autocomplete in `InteractionType.String()`, Indonesian locale.

## v1.3.1

Bug-fix release, third audit round (fresh code review + full REST sweep).
No API changes.

### Fixed
- state: presences from READY / GUILD_CREATE were never added to the
  presence index — `presenceAdd` appended duplicates instead of
  updating in place and `Presence()` missed READY entries. Indexes are
  now repopulated on READY and GuildAdd.
- state: message/presence indexes leaked for removed channels and
  guilds (ChannelRemove/GuildRemove now clean them).
- state: a stale message-index entry could wedge a channel forever
  after ChannelAdd replaced the channel with fresh data; stale entries
  are dropped and the index rebuilt.
- wsapi: the persistent zlib reader was closed concurrently with the
  listen goroutine using it (data race) — now synchronized via zMu.
- wsapi: raw once-handlers (`AddHandlerOnce(func(*Session, *Event))`)
  were silently skipped by the raw-dispatch fast path; hasHandlers
  now checks onceHandlers too.
- wsapi: with goccy/sonic, `Event.RawData` could alias the reused zlib
  output buffer and be corrupted by the next message; the raw event now
  owns a copy.
- sharding: closed Sessions were pinned in memory by handler entries;
  close drops their removers.
- REST: `WebhookDeleteWithToken` returned an error on every successful
  call (204 with empty body was unmarshaled).
- REST: `Entitlements` sent RFC3339 timestamps where Discord expects
  entitlement ID snowflakes; timestamps are now converted to snowflake
  boundary IDs (equivalent, since snowflakes are time-ordered).
- REST: rate-limit bucket IDs no longer fragment per query string for
  webhook execute, archived threads, thread members and reactions-
  remove-emoji; `WebhookMessageEdit` uses a consistent bucket;
  `EndpointVoice` double-slash fixed; VerificationLevel bounds message
  corrected (0-4).

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
