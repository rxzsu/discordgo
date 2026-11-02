package discordgo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPruningPreservesReferencedBucket(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("held-reference")
	shard := r.shardFor(b.Key)
	shard.Lock()
	r.pruneShardLocked(shard)
	shard.Unlock()
	if next := r.GetBucket(b.Key); next != b {
		t.Fatal("pruning created a second limiter while an old reference is still live")
	}
	runtime.KeepAlive(b)
}

func TestRemoveBucketPreservesActiveWindow(t *testing.T) {
	r := NewRatelimiter()
	b := r.LockBucket("active")
	r.RemoveBucket("active")
	if got := r.GetBucket("active"); got != b {
		t.Error("RemoveBucket replaced a locked bucket")
	}
	_ = b.Release(http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset-After": {"1"}})
	r.RemoveBucket("active")
	if got := r.GetBucket("active"); got != b {
		t.Error("RemoveBucket discarded an active cooldown")
	}
}

func TestWaitTimeHonorsLongerGlobalLimit(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("local")
	b.Lock()
	defer b.Unlock()
	b.Remaining = 0
	b.reset = time.Now().Add(30 * time.Millisecond)
	atomic.StoreInt64(r.global, time.Now().Add(time.Second).UnixNano())
	if wait := r.GetWaitTime(b, 1); wait < 900*time.Millisecond {
		t.Fatalf("local limit hid global limit: wait=%v", wait)
	}
}

func TestGlobalLimitNeverShortened(t *testing.T) {
	r := NewRatelimiter()
	first := r.LockBucket("early-response")
	_ = first.Release(http.Header{"X-Ratelimit-Global": {"true"}, "X-Ratelimit-Reset-After": {"1"}})
	// This represents another response already in flight when the first
	// global limit arrived, so lock its bucket without waiting.
	b := r.GetBucket("late-response")
	b.Lock()
	_ = b.Release(http.Header{"X-Ratelimit-Global": {"true"}, "X-Ratelimit-Reset-After": {"0.01"}})
	if wait := r.GetWaitTime(b, 1); wait < 900*time.Millisecond {
		t.Fatalf("later response shortened global limit: %v", wait)
	}
}

func TestRetryAfterHeaderSetsGlobalLimit(t *testing.T) {
	r := NewRatelimiter()
	b := r.LockBucket("global")
	if err := b.Release(http.Header{"X-Ratelimit-Global": {"true"}, "Retry-After": {"1"}}); err != nil {
		t.Fatal(err)
	}
	if wait := r.GetWaitTime(r.GetBucket("other"), 1); wait < 900*time.Millisecond {
		t.Fatalf("Retry-After did not set global cooldown: %v", wait)
	}
}

func TestWaitingBucketRechecksExtendedGlobalLimit(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("waiting")
	b.Remaining = 0
	b.reset = time.Now().Add(60 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		atomic.StoreInt64(r.global, time.Now().Add(time.Second).UnixNano())
	}()
	locked, err := r.LockBucketContext(ctx, b.Key)
	if locked != nil {
		_ = locked.Release(nil)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request bypassed extended global limit: %v", err)
	}
}

func Test429BodyUpdatesSharedCooldown(t *testing.T) {
	for _, global := range []bool{false, true} {
		name, payload := "local", `{"retry_after":1,"global":false}`
		if global {
			name, payload = "global", `{"retry_after":1,"global":true}`
		}
		t.Run(name, func(t *testing.T) {
			s, _ := New("")
			calls := 0
			s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})}
			_, err := s.RequestWithBucketID("GET", "https://example.com", nil, "first", WithRetryOnRatelimit(false))
			var limited *RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("expected RateLimitError, got %v", err)
			}
			key := "first"
			if global {
				key = "other"
			}
			_, err = s.RequestWithBucketID("GET", "https://example.com", nil, key, WithTimeout(10*time.Millisecond))
			if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
				t.Fatalf("shared cooldown bypassed: err=%v transport calls=%d", err, calls)
			}
		})
	}
}

func TestRejectInvalidRetryDurations(t *testing.T) {
	for _, value := range []string{"-1", "1e30"} {
		var rl TooManyRequests
		if err := Unmarshal([]byte(`{"retry_after":`+value+`}`), &rl); err == nil {
			t.Errorf("accepted invalid retry_after %s", value)
		}
	}
	for _, value := range []string{"NaN", "+Inf", "-1", "1e30"} {
		b := NewRatelimiter().LockBucket(value)
		if err := b.Release(http.Header{"X-Ratelimit-Reset-After": {value}}); err == nil {
			t.Errorf("accepted invalid reset-after %s", value)
		}
	}
}

func TestConcurrentPruneAndBucketUse(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("shared")
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				shard := r.shardFor(b.Key)
				shard.Lock()
				r.pruneShardLocked(shard)
				shard.Unlock()
				if got := r.LockBucket(b.Key); got != b {
					t.Error("concurrent pruning replaced a live bucket")
					_ = got.Release(nil)
					return
				} else {
					_ = got.Release(nil)
				}
			}
		}()
	}
	wg.Wait()
}

func TestPrunedBucketsCanBeCollected(t *testing.T) {
	r := NewRatelimiter()
	func() {
		for i := 0; i < 100; i++ {
			r.GetBucket(strconv.Itoa(i))
		}
		for i := range r.shards {
			s := &r.shards[i]
			s.Lock()
			r.pruneShardLocked(s)
			s.Unlock()
		}
	}()
	runtime.GC()
	for i := range r.shards {
		s := &r.shards[i]
		s.Lock()
		r.pruneShardLocked(s)
		if len(s.retired) != 0 {
			t.Errorf("shard %d retained %d unreachable keys", i, len(s.retired))
		}
		s.Unlock()
	}
}

func TestSubmillisecondRetryPrecision(t *testing.T) {
	var limited TooManyRequests
	if err := Unmarshal([]byte(`{"retry_after":0.0005,"global":true}`), &limited); err != nil {
		t.Fatal(err)
	}
	if limited.RetryAfter != 500*time.Microsecond || !limited.Global {
		t.Fatalf("lost retry precision or scope: %+v", limited)
	}
}

func TestCustomBucketHonorsGlobalHeaders(t *testing.T) {
	r := NewRatelimiter()
	b := r.LockBucket("channel//reactions//")
	if err := b.Release(http.Header{
		"X-Ratelimit-Global": {"true"}, "Retry-After": {"1"},
		"X-Ratelimit-Remaining": {"99"},
	}); err != nil {
		t.Fatal(err)
	}
	if b.Remaining != 0 {
		t.Fatal("server headers disabled the custom reaction limit")
	}
	if wait := r.GetWaitTime(r.GetBucket("other"), 1); wait < 900*time.Millisecond {
		t.Fatalf("custom bucket ignored global cooldown: %v", wait)
	}
}

func TestRetiredBucketRestoresActiveWindow(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("direct-reuse")
	r.RemoveBucket(b.Key)
	b.Lock()
	if err := b.Release(http.Header{
		"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset-After": {"1"},
	}); err != nil {
		t.Fatal(err)
	}
	if r.GetBucketIfPresent(b.Key) != b {
		t.Fatal("new cooldown was left weakly cached and could be collected")
	}
}

type blocked429Body struct {
	io.Reader
	started chan struct{}
	proceed chan struct{}
	once    sync.Once
}

func (b *blocked429Body) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.proceed
	return b.Reader.Read(p)
}

func (*blocked429Body) Close() error { return nil }

func Test429HoldsBucketUntilBodyIsDecoded(t *testing.T) {
	s, _ := New("")
	body := &blocked429Body{
		Reader:  strings.NewReader(`{"retry_after":1}`),
		started: make(chan struct{}), proceed: make(chan struct{}),
	}
	var calls atomic.Int32
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 429, Header: http.Header{}, Body: body}, nil
	})}
	first := make(chan error, 1)
	go func() {
		_, err := s.Request("GET", "https://example.com", nil, WithRetryOnRatelimit(false))
		first <- err
	}()
	waitStartupStage(t, body.started)
	_, err := s.Request("GET", "https://example.com", nil, WithTimeout(20*time.Millisecond))
	close(body.proceed)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Errorf("request bypassed an undecoded 429: err=%v calls=%d", err, calls.Load())
	}
	var limited *RateLimitError
	if err := waitStartupResult(t, first); !errors.As(err, &limited) {
		t.Fatalf("first request: %v", err)
	}
}

func TestExplicitRemovalSweepsCollectedReferences(t *testing.T) {
	r := NewRatelimiter()
	shard := &r.shards[0]
	remove := func(prefix string, count int) {
		for i := 0; count > 0; i++ {
			key := prefix + strconv.Itoa(i)
			if r.shardFor(key) != shard {
				continue
			}
			r.GetBucket(key)
			r.RemoveBucket(key)
			count--
		}
	}
	remove("old-", 600)
	runtime.GC()
	remove("new-", 512)
	shard.Lock()
	defer shard.Unlock()
	for key := range shard.retired {
		if strings.HasPrefix(key, "old-") {
			t.Fatal("explicit removals retained collected bucket references")
		}
	}
}
