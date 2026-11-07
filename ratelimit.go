package discordgo

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"weak"
)

// customRateLimit holds information for defining a custom rate limit
type customRateLimit struct {
	suffix   string
	requests int
	reset    time.Duration
}

// rateLimitShards is the number of independent lock+map shards. Spreading
// bucket lookups across shards keeps REST throughput high on busy bots:
// a single mutex over one map serializes every request's bucket lookup.
const rateLimitShards = 16

// rateLimitShard is one independent slice of the bucket space.
type rateLimitShard struct {
	sync.Mutex
	buckets               map[string]*Bucket
	retired               map[string]weak.Pointer[Bucket]
	retirementsSinceSweep int
}

// RateLimiter holds all ratelimit buckets
type RateLimiter struct {
	global           *int64
	shards           [rateLimitShards]rateLimitShard
	globalRateLimit  time.Duration //nolint:unused // reserved for future use
	customRateLimits []*customRateLimit
}

// NewRatelimiter returns a new RateLimiter
func NewRatelimiter() *RateLimiter {

	r := &RateLimiter{
		global: new(int64),
		customRateLimits: []*customRateLimit{
			{
				suffix:   "//reactions//",
				requests: 1,
				reset:    200 * time.Millisecond,
			},
		},
	}
	for i := range r.shards {
		r.shards[i].buckets = make(map[string]*Bucket)
	}
	return r
}

// shardFor returns the shard responsible for a bucket key.
func (r *RateLimiter) shardFor(key string) *rateLimitShard {
	var h uint32
	for i := 0; i < len(key); i++ {
		h = h*31 + uint32(key[i])
	}
	return &r.shards[h%rateLimitShards]
}

// GetBucket retrieves or creates a bucket
func (r *RateLimiter) GetBucket(key string) *Bucket {
	shard := r.shardFor(key)
	shard.Lock()
	defer shard.Unlock()

	if bucket, ok := shard.buckets[key]; ok {
		return bucket
	}

	// Opportunistically prune stale buckets so the map does not grow
	// without bound over a long-lived session.
	if len(shard.buckets) >= maxBucketsBeforePrune {
		r.pruneShardLocked(shard)
	}

	// A caller may still hold an expired bucket after it leaves the strong
	// cache. Reuse that object while it is alive so it never has two locks.
	if ref, ok := shard.retired[key]; ok {
		if b := ref.Value(); b != nil {
			delete(shard.retired, key)
			shard.buckets[key] = b
			b.retired.Store(false)
			return b
		}
		delete(shard.retired, key)
	}
	b := &Bucket{
		Remaining: 1,
		Key:       key,
		global:    r.global,
		shard:     shard,
	}

	// Check if there is a custom ratelimit set for this bucket ID.
	for _, rl := range r.customRateLimits {
		if strings.HasSuffix(b.Key, rl.suffix) {
			b.customRateLimit = rl
			break
		}
	}

	shard.buckets[key] = b
	return b
}

// maxBucketsBeforePrune is the bucket map size that triggers opportunistic
// pruning of expired buckets in GetBucket.
const maxBucketsBeforePrune = 512

// pruneShardLocked removes buckets whose rate limit window has certainly
// passed and which are not currently in use. It must be called with the
// shard's lock held.
func (r *RateLimiter) pruneShardLocked(shard *rateLimitShard) {
	now := time.Now()
	for key, b := range shard.buckets {
		// Skip buckets that are locked (in use) by an in-flight request.
		if !b.TryLock() {
			continue
		}
		expired := (b.reset.IsZero() || b.reset.Before(now)) && b.customRateLimit == nil
		if expired {
			r.retireBucketLocked(shard, key, b)
		}
		b.Unlock()
	}
	r.sweepRetiredLocked(shard)
}

func (r *RateLimiter) sweepRetiredLocked(shard *rateLimitShard) {
	for key, ref := range shard.retired {
		if ref.Value() == nil {
			delete(shard.retired, key)
		}
	}
	shard.retirementsSinceSweep = 0
}

func (r *RateLimiter) retireBucketLocked(shard *rateLimitShard, key string, b *Bucket) {
	if shard.retired == nil {
		shard.retired = make(map[string]weak.Pointer[Bucket])
	}
	shard.retired[key] = weak.Make(b)
	b.retired.Store(true)
	delete(shard.buckets, key)
	shard.retirementsSinceSweep++
	if shard.retirementsSinceSweep >= maxBucketsBeforePrune {
		r.sweepRetiredLocked(shard)
	}
}

// RemoveBucket evicts an idle bucket from the cache. Locked buckets and
// buckets with an active rate-limit window are retained. Existing references
// remain valid and will be reused by subsequent lookups until collected.
func (r *RateLimiter) RemoveBucket(key string) {
	shard := r.shardFor(key)
	shard.Lock()
	defer shard.Unlock()
	b := shard.buckets[key]
	if b == nil || !b.TryLock() {
		return
	}
	defer b.Unlock()
	if b.reset.IsZero() || !b.reset.After(time.Now()) {
		r.retireBucketLocked(shard, key, b)
	}
}

// GetWaitTime returns the longer of the bucket and global cooldowns.
// The caller must hold b's lock.
func (r *RateLimiter) GetWaitTime(b *Bucket, minRemaining int) time.Duration {
	now := time.Now()
	var wait time.Duration
	if b.Remaining < minRemaining && b.reset.After(now) {
		wait = b.reset.Sub(now)
	}
	if globalWait := time.Unix(0, atomic.LoadInt64(r.global)).Sub(now); globalWait > wait {
		wait = globalWait
	}
	return wait
}

// LockBucket Locks until a request can be made
func (r *RateLimiter) LockBucket(bucketID string) *Bucket {
	return r.LockBucketObject(r.GetBucket(bucketID))
}

// LockBucketObject Locks an already resolved bucket until a request can be made
func (r *RateLimiter) LockBucketObject(b *Bucket) *Bucket {
	b.Lock()

	for wait := r.GetWaitTime(b, 1); wait > 0; wait = r.GetWaitTime(b, 1) {
		time.Sleep(wait)
	}

	b.Remaining--
	return b
}

// LockBucketContext locks a bucket until a request can be made, waiting out
// the rate limit in a context-aware manner: if ctx is done first, the bucket
// is released and the context error is returned.
func (r *RateLimiter) LockBucketContext(ctx context.Context, bucketID string) (*Bucket, error) {
	return r.LockBucketObjectContext(ctx, r.GetBucket(bucketID))
}

// LockBucketObjectContext is LockBucketObject with context-aware waiting.
func (r *RateLimiter) LockBucketObjectContext(ctx context.Context, b *Bucket) (*Bucket, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Bucket embeds a public mutex, so retain compatibility with callers
	// using Lock directly without spawning a goroutine that can outlive ctx.
	if ctx.Done() == nil {
		b.Lock()
	} else if !b.TryLock() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for !b.TryLock() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if err := ctx.Err(); err != nil {
		b.Unlock()
		return nil, err
	}

	for wait := r.GetWaitTime(b, 1); wait > 0; wait = r.GetWaitTime(b, 1) {
		if err := sleepCtx(ctx, wait); err != nil {
			b.Unlock()
			return nil, err
		}
	}

	b.Remaining--
	return b, nil
}

// Canonical spellings of the rate-limit header names. Discord sends them in
// mixed case ("X-RateLimit-Remaining"), which is NOT the canonical MIME form,
// so headers.Get would re-canonicalize the key — an allocation — on every
// call of the hot Release path. Indexing the map directly with the
// precomputed canonical keys is equivalent (http.Header.Get is exactly
// headers[CanonicalMIMEHeaderKey(key)]) but allocation-free.
var (
	canonRateLimitGlobal     = http.CanonicalHeaderKey("X-RateLimit-Global")
	canonRateLimitRemaining  = http.CanonicalHeaderKey("X-RateLimit-Remaining")
	canonRateLimitReset      = http.CanonicalHeaderKey("X-RateLimit-Reset")
	canonRateLimitResetAfter = http.CanonicalHeaderKey("X-RateLimit-Reset-After")
)

// headerValue is equivalent to headers.Get(key) for an already canonical
// key: it avoids re-canonicalizing (and allocating) on every call.
func headerValue(headers http.Header, canonicalKey string) string {
	if v := headers[canonicalKey]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Bucket represents a ratelimit bucket, each bucket gets ratelimited individually (-global ratelimits)
type Bucket struct {
	sync.Mutex
	Key       string
	Remaining int
	limit     int //nolint:unused // populated from X-RateLimit-Limit in some deployments
	reset     time.Time
	global    *int64
	shard     *rateLimitShard
	retired   atomic.Bool

	lastReset       time.Time
	customRateLimit *customRateLimit
	Userdata        interface{}
}

// Release unlocks the bucket and reads the headers to update the buckets ratelimit info
// and locks up the whole thing in case if there's a global ratelimit.
func (b *Bucket) Release(headers http.Header) error {
	return b.release(headers, nil)
}

func (b *Bucket) release(headers http.Header, limited *TooManyRequests) error {
	defer b.Unlock()
	defer b.retainActiveWindow()
	if limited != nil {
		// Apply the 429 body last so optimistic headers cannot override it.
		defer func() {
			reset := time.Now().Add(limited.RetryAfter)
			b.Remaining = 0
			if reset.After(b.reset) {
				b.reset = reset
			}
			if limited.Global {
				extendGlobalLimit(b.global, reset.UnixNano())
			}
		}()
	}

	globalHeader := headerValue(headers, canonRateLimitGlobal)
	global := globalHeader == "1" || strings.EqualFold(globalHeader, "true")

	// Check if the bucket uses a custom ratelimiter
	if rl := b.customRateLimit; rl != nil {
		if time.Since(b.lastReset) >= rl.reset {
			b.Remaining = rl.requests - 1
			b.lastReset = time.Now()
		}
		if b.Remaining < 1 {
			b.reset = time.Now().Add(rl.reset)
		}
		if !global {
			return nil
		}
	}

	if headers == nil {
		return nil
	}

	remaining := headerValue(headers, canonRateLimitRemaining)
	reset := headerValue(headers, canonRateLimitReset)
	resetAfter := headerValue(headers, canonRateLimitResetAfter)
	if retryAfter := headers.Get("Retry-After"); retryAfter != "" {
		resetAfter = retryAfter
	}

	// Update global and per bucket reset time if the proper headers are available
	// If global is set, then it will block all buckets until after Retry-After
	// If Retry-After without global is provided it will use that for the new reset
	// time since it's more accurate than X-RateLimit-Reset.
	// If Retry-After is not provided, it will update the reset time from X-RateLimit-Reset
	if resetAfter != "" {
		parsedAfter, err := strconv.ParseFloat(resetAfter, 64)
		if err != nil {
			return err
		}

		delay, err := durationFromSeconds(parsedAfter)
		if err != nil {
			return err
		}
		resetAt := time.Now().Add(delay)

		// Lock either this single bucket or all buckets
		if global {
			extendGlobalLimit(b.global, resetAt.UnixNano())
		} else {
			b.reset = resetAt
		}
	} else if reset != "" {
		// Calculate the reset time by using the date header returned from discord
		discordTime, err := http.ParseTime(headers.Get("Date"))
		if err != nil {
			return err
		}

		unix, err := strconv.ParseFloat(reset, 64)
		if err != nil {
			return err
		}

		// Calculate the time until reset and add it to the current local time
		// some extra time is added because without it i still encountered 429's.
		// The added amount is the lowest amount that gave no 429's
		// in 1k requests
		whole, frac := math.Modf(unix)
		delta := time.Unix(int64(whole), 0).Add(time.Duration(frac*1000)*time.Millisecond).Sub(discordTime) + time.Millisecond*250
		resetAt := time.Now().Add(delta)
		if global {
			extendGlobalLimit(b.global, resetAt.UnixNano())
		} else {
			b.reset = resetAt
		}
	}

	// Update remaining if header is present
	if remaining != "" && b.customRateLimit == nil {
		parsedRemaining, err := strconv.ParseInt(remaining, 10, 32)
		if err != nil {
			return err
		}
		b.Remaining = int(parsedRemaining)
	}

	return nil
}

// A caller can reuse a bucket directly after it was pruned. Keep it strongly
// cached again if that request establishes a new cooldown, even after the
// caller drops the last external reference.
func (b *Bucket) retainActiveWindow() {
	if b.shard == nil || !b.retired.Load() || !b.reset.After(time.Now()) {
		return
	}
	b.shard.Lock()
	defer b.shard.Unlock()
	b.shard.buckets[b.Key] = b
	delete(b.shard.retired, b.Key)
	b.retired.Store(false)
}

// GetBucketIfPresent returns the bucket for the key if it exists, without
// creating one. It returns nil for unknown keys.
func (r *RateLimiter) GetBucketIfPresent(key string) *Bucket {
	shard := r.shardFor(key)
	shard.Lock()
	defer shard.Unlock()
	return shard.buckets[key]
}

// BucketCount returns the number of strongly cached buckets across all shards.
// Evicted weak references are not included.
func (r *RateLimiter) BucketCount() int {
	n := 0
	for i := range r.shards {
		r.shards[i].Lock()
		n += len(r.shards[i].buckets)
		r.shards[i].Unlock()
	}
	return n
}

// extendGlobalLimit never lets a later response shorten an existing cooldown.
func extendGlobalLimit(global *int64, reset int64) {
	for {
		previous := atomic.LoadInt64(global)
		if previous >= reset || atomic.CompareAndSwapInt64(global, previous, reset) {
			return
		}
	}
}

func durationFromSeconds(seconds float64) (time.Duration, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 || seconds >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, fmt.Errorf("invalid rate-limit duration: %v seconds", seconds)
	}
	return time.Duration(math.Ceil(seconds * float64(time.Second))), nil
}
