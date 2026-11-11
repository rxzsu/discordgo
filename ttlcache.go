// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements TTLCache, a generic in-memory cache with per-entry
// time-to-live, in-flight call de-duplication (singleflight semantics),
// hit/miss metrics, an optional janitor goroutine, TTL jitter and JSON
// persistence. It is standalone: it does not depend on State or Session,
// so it can be used for any purpose (ttlcache_state.go provides an
// optional Session/State oriented wrapper).

package discordgo

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sync"
	"time"
)

// ------------------------------------------------------------------------------------------------
// Public types
// ------------------------------------------------------------------------------------------------

// TTLCacheEvictReason explains why an entry left the cache.
type TTLCacheEvictReason string

// Reasons passed to TTLCacheOptions.OnEvict.
const (
	// TTLCacheEvictExpired: the entry outlived its TTL.
	TTLCacheEvictExpired TTLCacheEvictReason = "expired"
	// TTLCacheEvictCapacity: the entry was dropped because the cache was
	// over MaxEntries.
	TTLCacheEvictCapacity TTLCacheEvictReason = "capacity"
	// TTLCacheEvictDeleted: the entry was removed by Delete.
	TTLCacheEvictDeleted TTLCacheEvictReason = "deleted"
	// TTLCacheEvictCleared: the entry was removed by Clear.
	TTLCacheEvictCleared TTLCacheEvictReason = "cleared"
	// TTLCacheEvictOverwritten: Set replaced an existing entry. Only
	// delivered when the old entry had not expired yet.
	TTLCacheEvictOverwritten TTLCacheEvictReason = "overwritten"
)

// TTLCacheMetrics is a point-in-time snapshot of cache counters.
type TTLCacheMetrics struct {
	// Hits counts Get and GetOrLoad calls served from the cache.
	Hits int64
	// Misses counts Get and GetOrLoad calls that found nothing usable.
	Misses int64
	// Loads counts loader executions started by GetOrLoad.
	Loads int64
	// LoadErrors counts loader executions that returned an error.
	LoadErrors int64
	// Waiters counts GetOrLoad calls that joined an already running load
	// instead of starting a new one.
	Waiters int64
	// Evictions counts entries removed before expiry for capacity or by
	// explicit Delete/Clear (not TTL expiry).
	Evictions int64
	// Expired counts entries dropped because their TTL elapsed.
	Expired int64
}

// TTLCacheOptions configures a TTLCache. The zero value is usable with
// defaults applied by NewTTLCache; per-call TTLs (SetWithTTL,
// GetOrLoadWithTTL) override opts.TTL.
type TTLCacheOptions[K comparable, V any] struct {
	// TTL is the default lifetime of an entry. Zero or negative values
	// are replaced with DefaultTTLCacheTTL when the cache is created.
	TTL time.Duration
	// Jitter is the maximum amount subtracted randomly from the TTL of
	// each entry. Jitter spreads out expiry times for entries written in
	// bulk and prevents synchronized expiry stampedes. Zero disables it;
	// negative values use DefaultTTLCacheJitter (10% of TTL).
	Jitter time.Duration
	// MaxEntries caps the number of live entries. When the cap is
	// reached, expired entries are reclaimed first, then the entry
	// closest to expiry is evicted. Zero means unbounded.
	MaxEntries int
	// JanitorInterval is how often the janitor goroutine removes expired
	// entries. Zero means max(TTL/2, DefaultTTLCacheJanitorMin); a
	// negative value disables the janitor (expired entries are still
	// reclaimed lazily on Get/Set).
	JanitorInterval time.Duration
	// OnEvict, when non-nil, is called after an entry leaves the cache
	// for any reason. It runs outside the cache lock and must not call
	// back into the cache.
	OnEvict func(key K, value V, reason TTLCacheEvictReason)
	// Now overrides the clock, mainly for tests. Nil means time.Now.
	Now func() time.Time
}

// Defaults applied by NewTTLCache.
const (
	// DefaultTTLCacheTTL is the default entry lifetime.
	DefaultTTLCacheTTL = time.Minute
	// DefaultTTLCacheJitter is the default jitter fraction of TTL.
	DefaultTTLCacheJitter = 100 * time.Millisecond
	// DefaultTTLCacheJanitorMin is the minimum janitor interval.
	DefaultTTLCacheJanitorMin = 100 * time.Millisecond
)

// ttlCacheEntry is one cached value with its deadline.
type ttlCacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

// inflightCall tracks a running GetOrLoad loader so concurrent callers
// for the same key can share its result.
type inflightCall[V any] struct {
	done chan struct{}
	val  V
	err  error
}

// TTLCache is a generic cache with per-entry TTL. All methods are safe
// for concurrent use.
type TTLCache[K comparable, V any] struct {
	mu       sync.Mutex
	entries  map[K]*ttlCacheEntry[V]
	inflight map[K]*inflightCall[V]

	ttl             time.Duration
	jitter          time.Duration
	maxEntries      int
	janitorInterval time.Duration
	onEvict         func(K, V, TTLCacheEvictReason)
	now             func() time.Time

	janitorStop chan struct{}
	closeOnce   sync.Once

	hits       int64
	misses     int64
	loads      int64
	loadErrors int64
	waiters    int64
	evictions  int64
	expired    int64
}

// NewTTLCache creates a cache with the given options and starts its
// janitor goroutine unless JanitorInterval is negative. Call Close when
// the cache is no longer needed to stop the janitor.
func NewTTLCache[K comparable, V any](opts TTLCacheOptions[K, V]) *TTLCache[K, V] {
	c := &TTLCache[K, V]{
		entries:  make(map[K]*ttlCacheEntry[V]),
		inflight: make(map[K]*inflightCall[V]),
	}
	c.ttl = opts.TTL
	if c.ttl <= 0 {
		c.ttl = DefaultTTLCacheTTL
	}
	c.jitter = opts.Jitter
	if c.jitter < 0 {
		// 10% of the TTL.
		c.jitter = c.ttl / 10
	}
	c.maxEntries = opts.MaxEntries
	if c.maxEntries < 0 {
		c.maxEntries = 0
	}
	c.onEvict = opts.OnEvict
	c.now = opts.Now
	if c.now == nil {
		c.now = time.Now
	}
	c.janitorInterval = opts.JanitorInterval
	if c.janitorInterval == 0 {
		c.janitorInterval = c.ttl / 2
		if c.janitorInterval < DefaultTTLCacheJanitorMin {
			c.janitorInterval = DefaultTTLCacheJanitorMin
		}
	}
	c.janitorStop = make(chan struct{})
	if c.janitorInterval > 0 {
		go c.janitor()
	}
	return c
}

// ------------------------------------------------------------------------------------------------
// Basic operations
// ------------------------------------------------------------------------------------------------

// Get returns the value for key if it is present and not expired. An
// expired entry is reclaimed and reported as a miss.
func (c *TTLCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getLocked(key)
}

// getLocked is Get assuming the cache lock is held.
func (c *TTLCache[K, V]) getLocked(key K) (V, bool) {
	var zero V
	e, ok := c.entries[key]
	if !ok {
		c.misses++
		return zero, false
	}
	if c.now().After(e.expiresAt) {
		c.removeLocked(key, TTLCacheEvictExpired)
		c.misses++
		return zero, false
	}
	c.hits++
	return e.value, true
}

// Set stores value under key with the default TTL, replacing any
// previous value.
func (c *TTLCache[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, 0)
}

// SetWithTTL stores value under key with the given TTL; a zero or
// negative ttl falls back to the cache default.
func (c *TTLCache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.ttl
	}
	expiresAt := c.now().Add(ttl - c.jitterFor(ttl))

	c.mu.Lock()
	old, hadOld := c.entries[key]
	c.entries[key] = &ttlCacheEntry[V]{value: value, expiresAt: expiresAt}
	victims := c.enforceMaxLocked(key)
	c.mu.Unlock()

	if hadOld && c.now().Before(old.expiresAt) {
		c.notifyEvict(key, old.value, TTLCacheEvictOverwritten)
	}
	for _, v := range victims {
		c.notifyEvict(v.key, v.value, TTLCacheEvictCapacity)
	}
}

// Delete removes key and reports whether a live entry was removed.
func (c *TTLCache[K, V]) Delete(key K) bool {
	c.mu.Lock()
	e, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return false
	}
	expired := c.now().After(e.expiresAt)
	c.removeLocked(key, TTLCacheEvictDeleted)
	c.mu.Unlock()

	if !expired {
		c.notifyEvict(key, e.value, TTLCacheEvictDeleted)
	}
	return true
}

// Clear removes all entries.
func (c *TTLCache[K, V]) Clear() {
	c.mu.Lock()
	all := make(map[K]V, len(c.entries))
	for k, e := range c.entries {
		all[k] = e.value
	}
	c.entries = make(map[K]*ttlCacheEntry[V])
	c.evictions += int64(len(all))
	c.mu.Unlock()

	for k, v := range all {
		c.notifyEvict(k, v, TTLCacheEvictCleared)
	}
}

// Len returns the number of entries currently stored, including expired
// ones the janitor has not reclaimed yet.
func (c *TTLCache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Keys returns the keys of all live (non-expired) entries in no
// particular order.
func (c *TTLCache[K, V]) Keys() []K {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]K, 0, len(c.entries))
	now := c.now()
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

// Metrics returns a snapshot of the cache counters.
func (c *TTLCache[K, V]) Metrics() TTLCacheMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return TTLCacheMetrics{
		Hits:       c.hits,
		Misses:     c.misses,
		Loads:      c.loads,
		LoadErrors: c.loadErrors,
		Waiters:    c.waiters,
		Evictions:  c.evictions,
		Expired:    c.expired,
	}
}

// Close stops the janitor goroutine. The cache remains usable afterwards
// (expired entries are reclaimed lazily); Close is idempotent.
func (c *TTLCache[K, V]) Close() {
	c.closeOnce.Do(func() {
		if c.janitorStop != nil {
			close(c.janitorStop)
		}
	})
}

// ------------------------------------------------------------------------------------------------
// GetOrLoad (singleflight)
// ------------------------------------------------------------------------------------------------

// GetOrLoad returns the cached value for key, or runs load to fetch it,
// storing the result with the default TTL.
//
// Concurrent GetOrLoad calls for the same key share one loader execution
// (singleflight semantics): the first caller runs the loader, everyone
// else waits and receives the same value or error. Load errors are not
// cached.
func (c *TTLCache[K, V]) GetOrLoad(ctx context.Context, key K, load func(ctx context.Context, key K) (V, error)) (V, error) {
	return c.GetOrLoadWithTTL(ctx, key, 0, load)
}

// GetOrLoadWithTTL is GetOrLoad with an explicit TTL for the loaded
// value; zero falls back to the cache default.
func (c *TTLCache[K, V]) GetOrLoadWithTTL(ctx context.Context, key K, ttl time.Duration, load func(ctx context.Context, key K) (V, error)) (V, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && !c.now().After(e.expiresAt) {
		c.hits++
		v := e.value
		c.mu.Unlock()
		return v, nil
	}
	if _, ok := c.entries[key]; ok {
		// Expired: reclaim before loading.
		c.removeLocked(key, TTLCacheEvictExpired)
	} else {
		c.misses++
	}
	if call, ok := c.inflight[key]; ok {
		c.waiters++
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.val, call.err
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		}
	}
	call := &inflightCall[V]{done: make(chan struct{})}
	c.inflight[key] = call
	c.loads++
	c.mu.Unlock()

	c.runLoader(ctx, key, ttl, load, call)
	// This goroutine ran the loader itself, so the result is final.
	return call.val, call.err
}

// runLoader executes the loader outside the cache lock and publishes the
// result to the waiting callers.
func (c *TTLCache[K, V]) runLoader(ctx context.Context, key K, ttl time.Duration, load func(ctx context.Context, key K) (V, error), call *inflightCall[V]) {
	var (
		val V
		err error
	)
	func() {
		// A panicking loader must not strand the waiters.
		defer func() {
			if r := recover(); r != nil {
				err = wrapTTLCacheLoaderPanic(r)
				val = *new(V)
			}
		}()
		val, err = load(ctx, key)
	}()

	c.mu.Lock()
	var victims []ttlCacheEvicted[K, V]
	if err == nil {
		if ttl <= 0 {
			ttl = c.ttl
		}
		expiresAt := c.now().Add(ttl - c.jitterFor(ttl))
		old, hadOld := c.entries[key]
		c.entries[key] = &ttlCacheEntry[V]{value: val, expiresAt: expiresAt}
		victims = c.enforceMaxLocked(key)
		if hadOld && c.now().Before(old.expiresAt) {
			victims = append(victims, ttlCacheEvicted[K, V]{key: key, value: old.value, reason: TTLCacheEvictOverwritten})
		}
	} else {
		c.loadErrors++
	}
	call.val = val
	call.err = err
	delete(c.inflight, key)
	close(call.done)
	c.mu.Unlock()

	for _, v := range victims {
		c.notifyEvict(v.key, v.value, v.reason)
	}
}

// ErrTTLCacheLoaderPanicked is wrapped into the error returned by
// GetOrLoad when the loader panics; the cache stays consistent and the
// result is not stored.
var ErrTTLCacheLoaderPanicked = errors.New("ttlcache: loader panicked")

// wrapTTLCacheLoaderPanic converts a loader panic into an error.
func wrapTTLCacheLoaderPanic(r interface{}) error {
	if e, ok := r.(error); ok {
		return &TTLCacheLoaderPanicError{Recovered: e}
	}
	return &TTLCacheLoaderPanicError{PanicValue: r}
}

// TTLCacheLoaderPanicError is returned by GetOrLoad when the loader
// panics. It always unwraps to ErrTTLCacheLoaderPanicked and, when the
// panic value was an error, to that error as well.
type TTLCacheLoaderPanicError struct {
	// Recovered is the recovered value when it was an error.
	Recovered error
	// PanicValue is the recovered value when it was not an error.
	PanicValue interface{}
}

// Error implements the error interface.
func (e *TTLCacheLoaderPanicError) Error() string {
	if e.Recovered != nil {
		return "ttlcache: loader panicked: " + e.Recovered.Error()
	}
	return "ttlcache: loader panicked"
}

// Unwrap exposes the sentinel and the recovered error, if any.
func (e *TTLCacheLoaderPanicError) Unwrap() []error {
	if e.Recovered != nil {
		return []error{ErrTTLCacheLoaderPanicked, e.Recovered}
	}
	return []error{ErrTTLCacheLoaderPanicked}
}

// ------------------------------------------------------------------------------------------------
// Internals
// ------------------------------------------------------------------------------------------------

// jitterFor returns the random jitter to subtract from ttl. Callers must
// not hold the lock (uses the global RNG) but holding it is harmless.
func (c *TTLCache[K, V]) jitterFor(ttl time.Duration) time.Duration {
	if c.jitter <= 0 || ttl <= c.jitter {
		return 0
	}
	return time.Duration(rand.Int64N(int64(c.jitter)))
}

// ttlCacheEvicted records an entry removed by enforceMaxLocked or an
// overwrite so its OnEvict hook can run outside the cache lock.
type ttlCacheEvicted[K comparable, V any] struct {
	key    K
	value  V
	reason TTLCacheEvictReason
}

// enforceMaxLocked evicts entries when the cache is over MaxEntries.
// justStored is never evicted for capacity. Returns the evicted entries
// (values included) so callers can run OnEvict hooks after unlocking.
// Callers must hold the lock.
func (c *TTLCache[K, V]) enforceMaxLocked(justStored K) []ttlCacheEvicted[K, V] {
	if c.maxEntries <= 0 || len(c.entries) <= c.maxEntries {
		return nil
	}
	now := c.now()
	// Reclaim expired entries first: free and accurate.
	var evicted []ttlCacheEvicted[K, V]
	for k, e := range c.entries {
		if k == justStored {
			continue
		}
		if now.After(e.expiresAt) {
			delete(c.entries, k)
			c.expired++
			if e != nil {
				evicted = append(evicted, ttlCacheEvicted[K, V]{key: k, value: e.value, reason: TTLCacheEvictExpired})
			}
			if len(c.entries) <= c.maxEntries {
				return evicted
			}
		}
	}
	// Still over: evict the live entry closest to expiry.
	for len(c.entries) > c.maxEntries {
		var (
			victim    K
			victimExp time.Time
			victimVal V
			found     bool
		)
		for k, e := range c.entries {
			if k == justStored {
				continue
			}
			if !found || e.expiresAt.Before(victimExp) {
				victim, victimExp, victimVal, found = k, e.expiresAt, e.value, true
			}
		}
		if !found {
			break
		}
		delete(c.entries, victim)
		c.evictions++
		evicted = append(evicted, ttlCacheEvicted[K, V]{key: victim, value: victimVal, reason: TTLCacheEvictCapacity})
	}
	return evicted
}

// removeLocked deletes an entry, updating counters. Callers must hold the
// lock.
func (c *TTLCache[K, V]) removeLocked(key K, reason TTLCacheEvictReason) {
	if _, ok := c.entries[key]; !ok {
		return
	}
	delete(c.entries, key)
	switch reason {
	case TTLCacheEvictExpired:
		c.expired++
	default:
		c.evictions++
	}
}

// notifyEvict calls the OnEvict hook; a missing hook is a no-op.
func (c *TTLCache[K, V]) notifyEvict(key K, value V, reason TTLCacheEvictReason) {
	if c.onEvict == nil {
		return
	}
	// A panicking OnEvict callback must not kill the process or strand
	// the janitor goroutine: swallow the panic here.
	defer func() { _ = recover() }()
	c.onEvict(key, value, reason)
}

// janitor periodically removes expired entries.
func (c *TTLCache[K, V]) janitor() {
	ticker := time.NewTicker(c.janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.janitorStop:
			return
		case <-ticker.C:
			c.removeExpired()
		}
	}
}

// removeExpired drops all expired entries.
func (c *TTLCache[K, V]) removeExpired() {
	c.mu.Lock()
	now := c.now()
	var dead []K
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			dead = append(dead, k)
		}
	}
	removed := make([]struct {
		k K
		v V
	}, 0, len(dead))
	for _, k := range dead {
		e := c.entries[k]
		delete(c.entries, k)
		c.expired++
		if e != nil {
			removed = append(removed, struct {
				k K
				v V
			}{k, e.value})
		}
	}
	c.mu.Unlock()

	for _, r := range removed {
		c.notifyEvict(r.k, r.v, TTLCacheEvictExpired)
	}
}

// ------------------------------------------------------------------------------------------------
// JSON persistence
// ------------------------------------------------------------------------------------------------

// ttlCacheJSONEntry is the persisted form of one cache entry.
type ttlCacheJSONEntry[K any, V any] struct {
	Key       K         `json:"key"`
	Value     V         `json:"value"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SaveJSON serializes all live entries with their absolute expiry times,
// so a cache can be restored after a restart with the remaining TTLs
// intact. Keys and values are stored via encoding/json; both must be
// JSON-marshalable.
func (c *TTLCache[K, V]) SaveJSON() ([]byte, error) {
	c.mu.Lock()
	now := c.now()
	out := make([]ttlCacheJSONEntry[K, V], 0, len(c.entries))
	for k, e := range c.entries {
		if now.After(e.expiresAt) {
			continue
		}
		out = append(out, ttlCacheJSONEntry[K, V]{Key: k, Value: e.value, ExpiresAt: e.expiresAt})
	}
	c.mu.Unlock()
	return json.Marshal(out)
}

// LoadJSON replaces the cache contents with the serialized entries. All
// existing entries are dropped first; entries that already expired at
// load time are skipped. Counters are not restored.
func (c *TTLCache[K, V]) LoadJSON(data []byte) error {
	var in []ttlCacheJSONEntry[K, V]
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}

	c.mu.Lock()
	now := c.now()
	fresh := make(map[K]*ttlCacheEntry[V], len(in))
	var expired int
	for _, it := range in {
		if now.After(it.ExpiresAt) {
			expired++
			continue
		}
		fresh[it.Key] = &ttlCacheEntry[V]{value: it.Value, expiresAt: it.ExpiresAt}
	}
	c.entries = fresh
	c.mu.Unlock()
	return nil
}
