package discordgo

// Builtin middlewares for typed event handlers: panic recovery, logging,
// scope filtering, deduplication and per-key rate limiting. All of them are
// safe for concurrent use; the stateful ones (dedup, throttle) keep their
// caches mutex-guarded and purge lazily, so no janitor goroutine is needed.

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// ------------------------------------------------------------------------------------------------
// Panic recovery
// ------------------------------------------------------------------------------------------------

// RecoverTyped returns a middleware that recovers panics raised by the
// wrapped handler or by later middlewares, logging the value and the stack
// through the session logger. Without it a panicking handler takes down the
// whole process: handlers run on their own goroutines.
func RecoverTyped[E any]() TypedMiddleware[E] {
	return RecoverTypedWith[E](nil)
}

// RecoverTypedWith is RecoverTyped with a custom reporter. reporter, when
// non-nil, receives the session, the event and the recovered value and takes
// over the logging completely; when it is nil the panic is logged through
// the session logger at LogError.
func RecoverTypedWith[E any](reporter func(s *Session, e *E, recovered interface{})) TypedMiddleware[E] {
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return func(s *Session, e *E) {
			defer func() {
				if r := recover(); r != nil {
					if reporter != nil {
						reporter(s, e, r)
						return
					}
					s.log(LogError, "panic recovered in typed %T handler: %v\n%s", *new(E), r, debug.Stack())
				}
			}()
			next(s, e)
		}
	}
}

// ------------------------------------------------------------------------------------------------
// Logging
// ------------------------------------------------------------------------------------------------

// TypedLogConfig tunes LogTypedWith.
type TypedLogConfig struct {
	// Name identifies the handler in log lines. When empty the Go type
	// name of the event E is used.
	Name string
	// Slow, when > 0, escalates handler runs slower than Slow to
	// LogWarning. Zero defaults to one second; a negative value disables
	// the slow-path escalation.
	Slow time.Duration
}

// LogTyped returns a logging middleware named name that measures the wrapped
// handler's runtime: runs within the slow threshold (default 1s) are logged
// at LogDebug, slower runs at LogWarning.
func LogTyped[E any](name string) TypedMiddleware[E] {
	return LogTypedWith[E](TypedLogConfig{Name: name})
}

// LogTypedWith is LogTyped with full configuration.
func LogTypedWith[E any](cfg TypedLogConfig) TypedMiddleware[E] {
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("%T", *new(E))
	}
	slow := cfg.Slow
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return func(s *Session, e *E) {
			start := time.Now()
			next(s, e)
			d := time.Since(start)
			switch {
			case slow > 0 && d >= slow:
				s.log(LogWarning, "typed handler %s (%T) took %s", name, *new(E), d)
			default:
				s.log(LogDebug, "typed handler %s (%T) took %s", name, *new(E), d)
			}
		}
	}
}

// ------------------------------------------------------------------------------------------------
// Filtering
// ------------------------------------------------------------------------------------------------

// FilterTyped returns a middleware that only lets events through for which
// pred reports true; everything else exits the chain early. A nil pred lets
// everything through.
func FilterTyped[E any](pred func(s *Session, e *E) bool) TypedMiddleware[E] {
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return func(s *Session, e *E) {
			if pred != nil && !pred(s, e) {
				return
			}
			next(s, e)
		}
	}
}

// FilterEq returns a middleware that only lets events through whose value,
// extracted by get, equals want. A nil get drops everything.
func FilterEq[E any](get func(*E) string, want string) TypedMiddleware[E] {
	return FilterTyped[E](func(_ *Session, e *E) bool {
		return get != nil && get(e) == want
	})
}

// FilterOneOf is FilterEq matching any value of want.
func FilterOneOf[E any](get func(*E) string, want ...string) TypedMiddleware[E] {
	return FilterTyped[E](func(_ *Session, e *E) bool {
		if get == nil {
			return false
		}
		v := get(e)
		for _, w := range want {
			if v == w {
				return true
			}
		}
		return false
	})
}

// FilterNot inverts a filtering middleware: events the wrapped middleware
// would let through are dropped and vice versa. It observes the wrapped
// middleware by probing whether next got called, so it is intended for pure
// filters (not for middlewares with side effects).
func FilterNot[E any](mw TypedMiddleware[E]) TypedMiddleware[E] {
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return func(s *Session, e *E) {
			called := false
			wrapped := mw(func(*Session, *E) { called = true })
			wrapped(s, e)
			if !called {
				next(s, e)
			}
		}
	}
}

// ScopeFilter matches events by guild, channel and author/user ID. An empty
// field matches anything.
type ScopeFilter struct {
	GuildID   string
	ChannelID string
	AuthorID  string
}

// matches reports whether the triple satisfies the non-empty constraints.
func (f ScopeFilter) matches(guildID, channelID, authorID string) bool {
	if f.GuildID != "" && f.GuildID != guildID {
		return false
	}
	if f.ChannelID != "" && f.ChannelID != channelID {
		return false
	}
	if f.AuthorID != "" && f.AuthorID != authorID {
		return false
	}
	return true
}

// messageScope extracts (guildID, channelID, authorID) from a Message with
// nil safety; MessageDelete carries a partial Message with a nil Author.
func messageScope(m *Message) (guildID, channelID, authorID string) {
	if m == nil {
		return "", "", ""
	}
	if m.Author != nil {
		authorID = m.Author.ID
	}
	return m.GuildID, m.ChannelID, authorID
}

// FilterMessageEvents filters any event through a *Message accessor. Events
// with missing fields are dropped when the corresponding scope is set.
func FilterMessageEvents[E any](f ScopeFilter, get func(*E) *Message) TypedMiddleware[E] {
	return FilterTyped[E](func(_ *Session, e *E) bool {
		if e == nil {
			return false
		}
		g, c, a := messageScope(get(e))
		return f.matches(g, c, a)
	})
}

// FilterMessageCreateEvents filters MessageCreate events by scope.
func FilterMessageCreateEvents(f ScopeFilter) TypedMiddleware[MessageCreate] {
	return FilterMessageEvents[MessageCreate](f, func(e *MessageCreate) *Message { return e.Message })
}

// FilterMessageUpdateEvents filters MessageUpdate events by scope.
func FilterMessageUpdateEvents(f ScopeFilter) TypedMiddleware[MessageUpdate] {
	return FilterMessageEvents[MessageUpdate](f, func(e *MessageUpdate) *Message { return e.Message })
}

// FilterMessageDeleteEvents filters MessageDelete events by scope. Deleted
// messages carry no author, so AuthorID matching applies to nothing here.
func FilterMessageDeleteEvents(f ScopeFilter) TypedMiddleware[MessageDelete] {
	return FilterMessageEvents[MessageDelete](f, func(e *MessageDelete) *Message { return e.Message })
}

// FilterInteractionEvents filters InteractionCreate events by scope; the
// author field matches the invoking user (the guild member's user, or the
// User field for DM interactions).
func FilterInteractionEvents(f ScopeFilter) TypedMiddleware[InteractionCreate] {
	return FilterTyped[InteractionCreate](func(_ *Session, e *InteractionCreate) bool {
		if e == nil || e.Interaction == nil {
			return false
		}
		i := e.Interaction
		var author string
		switch {
		case i.Member != nil && i.Member.User != nil:
			author = i.Member.User.ID
		case i.User != nil:
			author = i.User.ID
		}
		return f.matches(i.GuildID, i.ChannelID, author)
	})
}

// ------------------------------------------------------------------------------------------------
// Deduplication
// ------------------------------------------------------------------------------------------------

// dedupMaxEntries bounds the dedup cache: when the bound is hit, expired
// entries are purged and, if the cache is still full, it is reset.
const dedupMaxEntries = 8192

// DedupTyped returns a middleware that drops events whose key was already
// seen within ttl, which is handy against duplicate gateway deliveries (for
// example after a RESUME replay). A nil key function or ttl <= 0 disables
// the filter entirely. The cache is mutex-guarded and safe for concurrent
// use.
func DedupTyped[E any](key func(*E) string, ttl time.Duration) TypedMiddleware[E] {
	if key == nil || ttl <= 0 {
		return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] { return next }
	}
	c := &dedupCache{seen: make(map[string]time.Time), ttl: ttl}
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return func(s *Session, e *E) {
			if c.allow(key(e), time.Now()) {
				next(s, e)
			}
		}
	}
}

// dedupCache tracks the last time each key was seen.
type dedupCache struct {
	mu        sync.Mutex
	seen      map[string]time.Time
	ttl       time.Duration
	lastPurge time.Time
}

// allow records key and reports whether it had not been seen within ttl.
func (c *dedupCache) allow(key string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.seen) >= dedupMaxEntries || (!c.lastPurge.IsZero() && now.Sub(c.lastPurge) >= c.ttl) {
		c.purgeLocked(now)
	}
	if t, ok := c.seen[key]; ok && now.Sub(t) < c.ttl {
		return false
	}
	c.seen[key] = now
	return true
}

// purgeLocked removes expired entries; when the cache is still full it is
// reset (an extreme-load safety valve that briefly weakens dedup instead of
// growing the map without bound).
func (c *dedupCache) purgeLocked(now time.Time) {
	for k, t := range c.seen {
		if now.Sub(t) >= c.ttl {
			delete(c.seen, k)
		}
	}
	if len(c.seen) >= dedupMaxEntries {
		c.seen = make(map[string]time.Time)
	}
	c.lastPurge = now
}

// ------------------------------------------------------------------------------------------------
// Rate limiting (anti-flood)
// ------------------------------------------------------------------------------------------------

// throttleMaxBuckets bounds the per-key bucket map, mirroring dedupMaxEntries.
const throttleMaxBuckets = 4096

// ThrottleConfig configures ThrottleTyped's per-key token bucket. The
// generic parameter carries the OnDrop callback's event type.
type ThrottleConfig[E any] struct {
	// Events is the sustained rate: Events events are allowed per Per.
	Events int
	// Per is the refill period for Events events.
	Per time.Duration
	// Burst is the bucket capacity, i.e. the short-term allowance. Zero
	// defaults to Events; negative values are treated as Events.
	Burst int
	// OnDrop, when set, is called for dropped events instead of the rest
	// of the chain.
	OnDrop func(s *Session, e *E)
}

// ThrottleTyped returns an anti-flood middleware holding a token bucket per
// key (typically a user or guild ID): each allowed event consumes one token,
// buckets refill at Events per Per and hold up to Burst tokens. Events
// without tokens are dropped. A nil key or an invalid rate (Events <= 0 or
// Per <= 0) disables the middleware. The limiter is mutex-guarded and safe
// for concurrent use.
func ThrottleTyped[E any](key func(*E) string, cfg ThrottleConfig[E]) TypedMiddleware[E] {
	if key == nil || cfg.Events <= 0 || cfg.Per <= 0 {
		return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] { return next }
	}
	burst := cfg.Burst
	if burst <= 0 {
		burst = cfg.Events
	}
	l := &throttleLimiter[E]{
		cfg:     cfg,
		burst:   float64(burst),
		rate:    float64(cfg.Events) / cfg.Per.Seconds(),
		buckets: make(map[string]*throttleBucket),
	}
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return func(s *Session, e *E) {
			if l.allow(key(e), time.Now()) {
				next(s, e)
				return
			}
			if l.cfg.OnDrop != nil {
				l.cfg.OnDrop(s, e)
			}
		}
	}
}

// throttleBucket is one key's token bucket.
type throttleBucket struct {
	tokens float64
	last   time.Time
}

// throttleLimiter guards the bucket map.
type throttleLimiter[E any] struct {
	cfg     ThrottleConfig[E]
	burst   float64
	rate    float64 // tokens per second
	mu      sync.Mutex
	buckets map[string]*throttleBucket
}

// allow consumes one token for key, refilling the bucket first.
func (l *throttleLimiter[E]) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) >= throttleMaxBuckets {
		l.purgeLocked(now)
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &throttleBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// purgeLocked drops buckets that could have fully refilled since their last
// use; when the map is still full it is reset as a safety valve.
func (l *throttleLimiter[E]) purgeLocked(now time.Time) {
	staleAfter := time.Duration(l.burst / l.rate * float64(time.Second))
	for k, b := range l.buckets {
		if now.Sub(b.last) > staleAfter {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) >= throttleMaxBuckets {
		l.buckets = make(map[string]*throttleBucket)
	}
}
