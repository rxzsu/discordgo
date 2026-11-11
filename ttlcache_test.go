package discordgo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a controllable clock for cache tests: it replaces the
// cache's Now so expiry timing tests need no sleeps.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// ------------------------------------------------------------------------------------------------
// Basics
// ------------------------------------------------------------------------------------------------

func TestTTLCacheSetGetDelete(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, JanitorInterval: -1})
	defer c.Close()

	if _, ok := c.Get("missing"); ok {
		t.Fatal("empty cache must miss")
	}
	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("Get(a) = %v, %v", v, ok)
	}
	c.Set("a", 2) // overwrite
	if v, _ := c.Get("a"); v != 2 {
		t.Fatalf("overwrite did not stick: %v", v)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
	if keys := c.Keys(); len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("Keys = %v", keys)
	}
	if !c.Delete("a") {
		t.Fatal("Delete should report a removed entry")
	}
	if c.Delete("a") {
		t.Fatal("second Delete should report nothing removed")
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d after delete", c.Len())
	}

	m := c.Metrics()
	if m.Hits != 2 || m.Misses != 1 || m.Evictions != 1 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestTTLCacheExpiry(t *testing.T) {
	clk := newFakeClock()
	c := NewTTLCache(TTLCacheOptions[string, string]{TTL: time.Minute, Now: clk.Now, JanitorInterval: -1})
	defer c.Close()

	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("fresh entry must hit")
	}
	clk.Advance(59 * time.Second)
	if _, ok := c.Get("k"); !ok {
		t.Fatal("entry must still be live at 59s")
	}
	clk.Advance(2 * time.Second)
	if _, ok := c.Get("k"); ok {
		t.Fatal("entry must be expired at 61s")
	}
	if c.Len() != 0 {
		t.Fatalf("expired entry not reclaimed: Len = %d", c.Len())
	}
	if m := c.Metrics(); m.Expired != 1 {
		t.Fatalf("expired counter = %d, want 1", m.Expired)
	}
}

func TestTTLCacheSetWithTTL(t *testing.T) {
	clk := newFakeClock()
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, Now: clk.Now, JanitorInterval: -1})
	defer c.Close()

	c.SetWithTTL("short", 1, 10*time.Second)
	c.SetWithTTL("long", 2, time.Hour)
	clk.Advance(30 * time.Second)
	if _, ok := c.Get("short"); ok {
		t.Fatal("short entry must expire after 10s")
	}
	if _, ok := c.Get("long"); !ok {
		t.Fatal("long entry must survive 30s")
	}
}

func TestTTLCacheJitterBounds(t *testing.T) {
	clk := newFakeClock()
	c := NewTTLCache(TTLCacheOptions[string, int]{
		TTL:             100 * time.Millisecond,
		Jitter:          40 * time.Millisecond,
		Now:             clk.Now,
		JanitorInterval: -1,
	})
	defer c.Close()

	for i := 0; i < 50; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	// Inspect persisted expiries: each must be in [now+60ms, now+100ms].
	data, err := c.SaveJSON()
	if err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}
	var entries []struct {
		Key       string    `json:"key"`
		Value     int       `json:"value"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := Unmarshal(data, &entries); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}
	if len(entries) != 50 {
		t.Fatalf("saved %d entries, want 50", len(entries))
	}
	now := clk.Now()
	for _, e := range entries {
		remaining := e.ExpiresAt.Sub(now)
		if remaining < 60*time.Millisecond || remaining > 100*time.Millisecond {
			t.Fatalf("entry %q remaining TTL %v out of jitter bounds [60ms, 100ms]", e.Key, remaining)
		}
	}
}

func TestTTLCacheJanitorReclaims(t *testing.T) {
	clk := newFakeClock()
	c := NewTTLCache(TTLCacheOptions[string, int]{
		TTL:             20 * time.Millisecond,
		Now:             clk.Now,
		JanitorInterval: 10 * time.Millisecond,
	})
	defer c.Close()

	for i := 0; i < 10; i++ {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	clk.Advance(25 * time.Millisecond)
	// The janitor uses a real timer while the clock is fake; force one
	// sweep deterministically by calling the internal sweep.
	c.removeExpired()
	if c.Len() != 0 {
		t.Fatalf("Len = %d after janitor sweep, want 0", c.Len())
	}
	if m := c.Metrics(); m.Expired != 10 {
		t.Fatalf("expired counter = %d, want 10", m.Expired)
	}
}

func TestTTLCacheRealTimerJanitor(t *testing.T) {
	// A short real-time check that the janitor goroutine works end to end.
	c := NewTTLCache(TTLCacheOptions[string, int]{
		TTL:             30 * time.Millisecond,
		JanitorInterval: 10 * time.Millisecond,
	})
	defer c.Close()

	c.Set("k", 1)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && c.Len() > 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Len() != 0 {
		t.Fatalf("janitor did not reclaim: Len = %d", c.Len())
	}
}

func TestTTLCacheCloseIdempotent(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute})
	c.Set("k", 1)
	c.Close()
	c.Close() // must not panic
	// The cache remains usable: expired entries are reclaimed lazily.
	if v, ok := c.Get("k"); !ok || v != 1 {
		t.Fatalf("Get after Close = %v, %v", v, ok)
	}
}

func TestTTLCacheClear(t *testing.T) {
	var cleared []int
	c := NewTTLCache(TTLCacheOptions[int, string]{
		TTL:             time.Minute,
		JanitorInterval: -1,
		OnEvict: func(key int, value string, reason TTLCacheEvictReason) {
			if reason == TTLCacheEvictCleared {
				cleared = append(cleared, key)
			}
		},
	})
	defer c.Close()

	for i := 0; i < 5; i++ {
		c.Set(i, fmt.Sprintf("v%d", i))
	}
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Len = %d after Clear", c.Len())
	}
	if len(cleared) != 5 {
		t.Fatalf("OnEvict cleared calls = %d, want 5", len(cleared))
	}
}

// ------------------------------------------------------------------------------------------------
// GetOrLoad (singleflight)
// ------------------------------------------------------------------------------------------------

func TestTTLCacheGetOrLoadSingleflight(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, JanitorInterval: -1})
	defer c.Close()

	var loads atomic.Int64
	var arrivals atomic.Int64
	const callers = 10
	loader := func(_ context.Context, key string) (int, error) {
		loads.Add(1)
		// Block until every caller has entered GetOrLoad, so all of them
		// observe the in-flight call instead of racing the cache fill.
		// The grace sleep covers the gap between a caller incrementing
		// arrivals and actually taking the cache lock.
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && arrivals.Load() < callers {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		return 42, nil
	}

	results := make([]int, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			arrivals.Add(1)
			results[i], errs[i] = c.GetOrLoad(context.Background(), "k", loader)
		}(i)
	}
	wg.Wait()

	for i := 0; i < callers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d error: %v", i, errs[i])
		}
		if results[i] != 42 {
			t.Fatalf("caller %d got %d, want 42", i, results[i])
		}
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("loader ran %d times, want 1", n)
	}
	m := c.Metrics()
	if m.Loads != 1 || m.Waiters != callers-1 || m.Hits != 0 || m.Misses != callers {
		t.Fatalf("metrics = %+v, want Loads=1 Waiters=%d Misses=%d", m, callers-1, callers)
	}

	// Second call is served from cache.
	if v, err := c.GetOrLoad(context.Background(), "k", loader); err != nil || v != 42 {
		t.Fatalf("cached GetOrLoad = %v, %v", v, err)
	}
	if n := loads.Load(); n != 1 {
		t.Fatalf("loader ran %d times after cache hit, want 1", n)
	}
}

func TestTTLCacheGetOrLoadError(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, JanitorInterval: -1})
	defer c.Close()

	wantErr := errors.New("boom")
	_, err := c.GetOrLoad(context.Background(), "k", func(context.Context, string) (int, error) {
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected loader error, got %v", err)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("failed load must not be cached")
	}
	m := c.Metrics()
	if m.Loads != 1 || m.LoadErrors != 1 {
		t.Fatalf("metrics = %+v", m)
	}

	// A retry after the error runs the loader again.
	v, err := c.GetOrLoad(context.Background(), "k", func(context.Context, string) (int, error) {
		return 7, nil
	})
	if err != nil || v != 7 {
		t.Fatalf("retry = %v, %v", v, err)
	}
}

func TestTTLCacheGetOrLoadWaiterContextCancel(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, JanitorInterval: -1})
	defer c.Close()

	release := make(chan struct{})
	// Guaranteed unblock of the leader goroutine even if the test exits
	// early through t.Fatalf.
	defer close(release)
	go func() {
		_, err := c.GetOrLoad(context.Background(), "k", func(context.Context, string) (int, error) {
			<-release
			return 1, nil
		})
		if err != nil {
			t.Errorf("leader error: %v", err)
		}
	}()
	// Wait until the leader registered its in-flight call.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		n := len(c.inflight)
		c.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetOrLoad(ctx, "k", func(context.Context, string) (int, error) {
		t.Error("cancelled waiter must not run the loader")
		return 0, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestTTLCacheGetOrLoadLeaderContextCancel(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, JanitorInterval: -1})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.GetOrLoad(ctx, "k", func(_ context.Context, _ string) (int, error) {
		cancel()
		<-ctx.Done()
		return 0, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// The in-flight call must be deregistered so a later load can run.
	if v, err := c.GetOrLoad(context.Background(), "k", func(context.Context, string) (int, error) {
		return 9, nil
	}); err != nil || v != 9 {
		t.Fatalf("post-cancel load = %v, %v", v, err)
	}
}

func TestTTLCacheLoaderPanic(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[string, int]{TTL: time.Minute, JanitorInterval: -1})
	defer c.Close()

	_, err := c.GetOrLoad(context.Background(), "k", func(context.Context, string) (int, error) {
		panic("kaboom")
	})
	var perr *TTLCacheLoaderPanicError
	if !errors.As(err, &perr) {
		t.Fatalf("expected *TTLCacheLoaderPanicError, got %v", err)
	}
	if !errors.Is(err, ErrTTLCacheLoaderPanicked) {
		t.Fatalf("error should unwrap to ErrTTLCacheLoaderPanicked: %v", err)
	}
	if c.Len() != 0 {
		t.Fatalf("panicked load must not be cached, Len = %d", c.Len())
	}
	// In-flight slot must be free again.
	c.mu.Lock()
	n := len(c.inflight)
	c.mu.Unlock()
	if n != 0 {
		t.Fatalf("in-flight map not cleaned: %d", n)
	}
}

// ------------------------------------------------------------------------------------------------
// Capacity and eviction hooks
// ------------------------------------------------------------------------------------------------

func TestTTLCacheMaxEntries(t *testing.T) {
	clk := newFakeClock()
	var evicted []string
	c := NewTTLCache(TTLCacheOptions[string, int]{
		TTL:             time.Minute,
		MaxEntries:      2,
		Now:             clk.Now,
		JanitorInterval: -1,
		OnEvict: func(key string, value int, reason TTLCacheEvictReason) {
			if reason == TTLCacheEvictCapacity {
				evicted = append(evicted, key)
			}
		},
	})
	defer c.Close()

	c.SetWithTTL("a", 1, 10*time.Second) // expires first
	c.SetWithTTL("b", 2, time.Hour)
	c.Set("c", 3) // over capacity: a is nearest expiry and gets evicted
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("a should have been evicted for capacity")
	}
	if _, ok := c.Get("b"); !ok {
		t.Fatal("b must remain")
	}
	if len(evicted) != 1 || evicted[0] != "a" {
		t.Fatalf("capacity evictions = %v, want [a]", evicted)
	}
}

func TestTTLCacheOnEvictReasons(t *testing.T) {
	type ev struct {
		key    string
		reason TTLCacheEvictReason
	}
	var events []ev
	c := NewTTLCache(TTLCacheOptions[string, int]{
		TTL:             time.Minute,
		JanitorInterval: -1,
		OnEvict: func(key string, _ int, reason TTLCacheEvictReason) {
			events = append(events, ev{key, reason})
		},
	})
	defer c.Close()

	c.Set("x", 1)
	c.Set("x", 2) // overwrite
	c.Delete("x") // deleted
	c.Clear()     // nothing left, no events
	want := []TTLCacheEvictReason{TTLCacheEvictOverwritten, TTLCacheEvictDeleted}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i, w := range want {
		if events[i].reason != w || events[i].key != "x" {
			t.Fatalf("event %d = %+v, want %s/x", i, events[i], w)
		}
	}
}

// ------------------------------------------------------------------------------------------------
// JSON persistence
// ------------------------------------------------------------------------------------------------

func TestTTLCacheSaveLoadJSON(t *testing.T) {
	clk := newFakeClock()
	src := NewTTLCache(TTLCacheOptions[string, int]{
		TTL:             time.Hour,
		Now:             clk.Now,
		JanitorInterval: -1,
	})
	src.Set("live", 1)
	src.SetWithTTL("dying", 2, 10*time.Second)
	clk.Advance(20 * time.Second) // "dying" is now expired

	data, err := src.SaveJSON()
	if err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}
	var raw []map[string]interface{}
	if err := Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}
	if len(raw) != 1 {
		t.Fatalf("snapshot must skip expired entries, got %d", len(raw))
	}

	dst := NewTTLCache(TTLCacheOptions[string, int]{Now: clk.Now, JanitorInterval: -1})
	defer dst.Close()
	if err := dst.LoadJSON(data); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if dst.Len() != 1 {
		t.Fatalf("Len after load = %d, want 1", dst.Len())
	}
	if v, ok := dst.Get("live"); !ok || v != 1 {
		t.Fatalf("Get(live) = %v, %v", v, ok)
	}
	// Remaining TTL preserved: entry saved at t0 with 1h TTL, 20s elapsed.
	clk.Advance(50 * time.Minute)
	if _, ok := dst.Get("live"); !ok {
		t.Fatal("loaded entry must keep its remaining TTL")
	}
	clk.Advance(15 * time.Minute)
	if _, ok := dst.Get("live"); ok {
		t.Fatal("loaded entry must expire once the original TTL elapsed")
	}

	if err := dst.LoadJSON([]byte("garbage")); err == nil {
		t.Fatal("expected error on garbage input")
	}
}

// ------------------------------------------------------------------------------------------------
// Concurrency
// ------------------------------------------------------------------------------------------------

func TestTTLCacheRace(t *testing.T) {
	c := NewTTLCache(TTLCacheOptions[int, int]{
		TTL:             10 * time.Millisecond,
		MaxEntries:      64,
		JanitorInterval: 5 * time.Millisecond,
	})
	defer c.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	time.AfterFunc(120*time.Millisecond, func() { close(stop) })

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := (w*13 + i) % 100
				switch i % 5 {
				case 0:
					c.Set(key, i)
				case 1:
					_, _ = c.Get(key)
				case 2:
					_, _ = c.GetOrLoad(context.Background(), key, func(context.Context, int) (int, error) {
						return i, nil
					})
				case 3:
					c.Delete(key)
				default:
					_ = c.Metrics()
				}
				i++
			}
		}(w)
	}
	wg.Wait()
}

func TestTTLCacheRaceSaveLoad(t *testing.T) {
	clk := newFakeClock()
	c := NewTTLCache(TTLCacheOptions[int, int]{TTL: time.Hour, Now: clk.Now, JanitorInterval: -1})
	defer c.Close()

	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.Set(i%16, i)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = c.Get(i % 16)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			data, err := c.SaveJSON()
			if err != nil {
				t.Errorf("SaveJSON: %v", err)
				return
			}
			scratch := NewTTLCache(TTLCacheOptions[int, int]{Now: clk.Now, JanitorInterval: -1})
			if err := scratch.LoadJSON(data); err != nil {
				t.Errorf("LoadJSON: %v", err)
				return
			}
			scratch.Close()
		}
		close(done)
	}()
	wg.Wait()
	<-done
}

// ------------------------------------------------------------------------------------------------
// CachedSession wrapper
// ------------------------------------------------------------------------------------------------

// fireTTLCacheEvent dispatches a gateway event through the session's real
// handler path (SyncEvents makes it synchronous).
func fireTTLCacheEvent(t *testing.T, s *Session, eventType string, payload interface{}) {
	t.Helper()
	s.SyncEvents = true
	s.handleEvent(eventType, payload)
}

func TestCachedSessionChannelCaching(t *testing.T) {
	var hits atomic.Int64
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/channels/c1" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "c1", "name": "general", "type": 0}`))
	}))
	defer cleanup()

	cs := NewCachedSession(s, CachedSessionOptions{
		ChannelTTL:            time.Minute,
		MemberTTL:             -1, // member cache disabled for this test
		DisableAutoInvalidate: true,
	})
	defer cs.Close()

	ctx := context.Background()
	ch1, err := cs.Channel(ctx, "c1")
	if err != nil {
		t.Fatalf("Channel: %v", err)
	}
	ch2, err := cs.Channel(ctx, "c1")
	if err != nil {
		t.Fatalf("Channel (cached): %v", err)
	}
	if ch1.Name != "general" || ch2.Name != ch1.Name {
		t.Fatalf("unexpected channels: %+v / %+v", ch1, ch2)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("REST hits = %d, want 1 (second call served from cache)", n)
	}

	// Invalidate: next call goes to REST again.
	cs.InvalidateChannel("c1")
	if _, err := cs.Channel(ctx, "c1"); err != nil {
		t.Fatalf("Channel after invalidate: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("REST hits after invalidate = %d, want 2", n)
	}
}

func TestCachedSessionGuildMemberCaching(t *testing.T) {
	var hits atomic.Int64
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/guilds/g1/members/u1" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"guild_id": "g1", "user": {"id": "u1", "username": "member"}, "nick": "nick"}`))
	}))
	defer cleanup()

	cs := NewCachedSession(s, CachedSessionOptions{
		ChannelTTL:            -1,
		MemberTTL:             time.Minute,
		DisableAutoInvalidate: true,
	})
	defer cs.Close()

	ctx := context.Background()
	m1, err := cs.GuildMember(ctx, "g1", "u1")
	if err != nil {
		t.Fatalf("GuildMember: %v", err)
	}
	if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
		t.Fatalf("GuildMember (cached): %v", err)
	}
	if m1.User == nil || m1.User.Username != "member" {
		t.Fatalf("unexpected member: %+v", m1)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("REST hits = %d, want 1", n)
	}

	// Invalidate, then let concurrent GetOrLoad calls share one REST
	// fetch (singleflight deduplication on a cold key).
	cs.InvalidateMember("g1", "u1")
	var wg sync.WaitGroup
	hits.Store(0)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
				t.Errorf("concurrent GuildMember: %v", err)
			}
		}()
	}
	wg.Wait()
	if n := hits.Load(); n != 1 {
		t.Fatalf("concurrent REST hits = %d, want 1", n)
	}
}

func TestCachedSessionAutoInvalidate(t *testing.T) {
	var hits atomic.Int64
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "c1", "name": "renamed", "type": 0}`))
	}))
	defer cleanup()

	cs := NewCachedSession(s, CachedSessionOptions{
		ChannelTTL: time.Minute,
		MemberTTL:  time.Minute,
	})
	defer cs.Close()

	ctx := context.Background()
	if _, err := cs.Channel(ctx, "c1"); err != nil {
		t.Fatalf("Channel: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("REST hits = %d, want 1", n)
	}

	// A gateway CHANNEL_UPDATE must drop the cached channel.
	fireTTLCacheEvent(t, s, channelUpdateEventType, &ChannelUpdate{
		Channel: &Channel{ID: "c1", Name: "renamed", Type: ChannelTypeGuildText},
	})
	if _, err := cs.Channel(ctx, "c1"); err != nil {
		t.Fatalf("Channel after CHANNEL_UPDATE: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("REST hits after CHANNEL_UPDATE = %d, want 2", n)
	}
}

func TestCachedSessionMemberAutoInvalidate(t *testing.T) {
	var hits atomic.Int64
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"guild_id": "g1", "user": {"id": "u1", "username": "m"}, "nick": "n"}`))
	}))
	defer cleanup()

	cs := NewCachedSession(s, CachedSessionOptions{MemberTTL: time.Minute})
	defer cs.Close()

	ctx := context.Background()
	if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
		t.Fatalf("GuildMember: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("REST hits = %d, want 1", n)
	}

	// GuildMemberUpdate invalidates exactly that member.
	fireTTLCacheEvent(t, s, guildMemberUpdateEventType, &GuildMemberUpdate{
		Member: &Member{GuildID: "g1", User: &User{ID: "u1", Username: "m"}, Nick: "new"},
	})
	if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
		t.Fatalf("GuildMember after GUILD_MEMBER_UPDATE: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("REST hits after GUILD_MEMBER_UPDATE = %d, want 2", n)
	}
}

func TestCachedSessionInvalidateGuild(t *testing.T) {
	var hits atomic.Int64
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"guild_id": "g1", "user": {"id": "u1", "username": "m"}}`))
	}))
	defer cleanup()

	cs := NewCachedSession(s, CachedSessionOptions{
		ChannelTTL: -1,
		MemberTTL:  time.Hour,
		MaxMembers: 100,
	})
	defer cs.Close()

	ctx := context.Background()
	// Members of two guilds.
	if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
		t.Fatalf("GuildMember g1: %v", err)
	}
	if _, err := cs.GuildMember(ctx, "g2", "u1"); err != nil {
		t.Fatalf("GuildMember g2: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("REST hits = %d, want 2", n)
	}

	// Cache is warm: no new REST calls.
	if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
		t.Fatalf("cached GuildMember: %v", err)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("REST hits after cached call = %d, want 2", n)
	}

	// GUILD_DELETE invalidates only that guild's members.
	fireTTLCacheEvent(t, s, guildDeleteEventType, &GuildDelete{
		Guild: &Guild{ID: "g1"},
	})
	if _, err := cs.GuildMember(ctx, "g1", "u1"); err != nil {
		t.Fatalf("GuildMember g1 after GUILD_DELETE: %v", err)
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("REST hits after GUILD_DELETE = %d, want 3", n)
	}
	// g2's member survived the guild-scoped invalidation.
	if _, err := cs.GuildMember(ctx, "g2", "u1"); err != nil {
		t.Fatalf("GuildMember g2 after GUILD_DELETE: %v", err)
	}
	if n := hits.Load(); n != 3 {
		t.Fatalf("REST hits for untouched guild = %d, want 3", n)
	}
}

func TestCachedSessionStatsAndDisabledCaches(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "c1", "name": "x", "type": 0}`))
	}))
	defer cleanup()

	cs := NewCachedSession(s, CachedSessionOptions{
		ChannelTTL:            -1, // disabled
		MemberTTL:             -1, // disabled
		DisableAutoInvalidate: true,
	})
	defer cs.Close()

	if cs.ChannelCache() != nil || cs.MemberCache() != nil {
		t.Fatal("caches must be nil when disabled")
	}
	// Lookups pass straight through to the session.
	if _, err := cs.Channel(context.Background(), "c1"); err != nil {
		t.Fatalf("passthrough Channel: %v", err)
	}

	st := cs.Stats()
	if st.ChannelLen != 0 || st.MemberLen != 0 {
		t.Fatalf("stats = %+v", st)
	}
}
