package discordgo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRESTErrorCode(t *testing.T) {
	r := RESTError{}
	if code := r.Code(); code != 0 {
		t.Fatalf("expected Code() = 0 for empty RESTError, got %d", code)
	}

	r = RESTError{Message: &APIErrorMessage{Code: 50013, Message: "Missing Permissions"}}
	if code := r.Code(); code != 50013 {
		t.Fatalf("expected Code() = 50013, got %d", code)
	}
}

func TestSleepCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := sleepCtx(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("sleepCtx should return early on cancel, took %v", elapsed)
	}
}

func TestSleepCtxCompletes(t *testing.T) {
	if err := sleepCtx(context.Background(), 5*time.Millisecond); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestWithQuery(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/api?a=1", nil)
	cfg := &RequestConfig{Request: req}
	WithQuery("limit", "10")(cfg)
	WithQuery("a", "2")(cfg)

	if got := cfg.Request.URL.Query().Get("limit"); got != "10" {
		t.Fatalf("expected limit=10, got %q", got)
	}
	if got := cfg.Request.URL.Query().Get("a"); got != "2" {
		t.Fatalf("expected a=2, got %q", got)
	}
}

func TestWithContextAbortsRateLimitRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"retry_after": 10.0, "global": false}`))
	}))
	defer srv.Close()

	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // request should fail immediately, not after the 10s retry_after

	_, err = s.RequestWithBucketID("GET", srv.URL+"/foo", nil, srv.URL+"/foo", WithContext(ctx))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from rate limit retry, got %v", err)
	}
}

func TestWithTimeoutAbortsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until client hangs up
	}))
	defer srv.Close()

	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.RequestWithBucketID("GET", srv.URL+"/foo", nil, srv.URL+"/foo", WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestPollIntentsInDefaults(t *testing.T) {
	if IntentsAllWithoutPrivileged&IntentGuildMessagePolls == 0 {
		t.Fatal("IntentsAllWithoutPrivileged should include IntentGuildMessagePolls")
	}
	if IntentsAllWithoutPrivileged&IntentDirectMessagePolls == 0 {
		t.Fatal("IntentsAllWithoutPrivileged should include IntentDirectMessagePolls")
	}
	if IntentsAllWithoutPrivileged&IntentsAutoModerationConfiguration == 0 {
		t.Fatal("IntentsAllWithoutPrivileged should include IntentsAutoModerationConfiguration")
	}
}

func TestFailedHeartbeatAcksValue(t *testing.T) {
	// Must be a plain interval count, not a duration like 5ms (which made
	// the reconnect threshold ~200ms and caused spurious reconnects).
	if FailedHeartbeatAcks != 5 {
		t.Fatalf("expected FailedHeartbeatAcks = 5, got %v", FailedHeartbeatAcks)
	}
}

func TestPollVotersUnmarshal(t *testing.T) {
	body := []byte(`{"answer_id": 2, "users": [{"id": "123", "username": "tester"}]}`)
	answer := struct {
		AnswerID int     `json:"answer_id"`
		Users    []*User `json:"users"`
	}{}
	if err := unmarshal(body, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.AnswerID != 2 || len(answer.Users) != 1 || answer.Users[0].Username != "tester" {
		t.Fatalf("unexpected unmarshal result: %+v", answer)
	}
	if !strings.Contains(answer.Users[0].ID, "123") {
		t.Fatalf("unexpected user id: %q", answer.Users[0].ID)
	}
}

func TestLockBucketContextCancelled(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("test-bucket")
	b.Remaining = 0
	b.reset = time.Now().Add(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := r.LockBucketContext(ctx, "test-bucket")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("LockBucketContext should return early on cancel, took %v", elapsed)
	}

	// The bucket must have been released so it can be locked again.
	if !b.TryLock() {
		t.Fatal("bucket was not released after context cancellation")
	}
	b.Unlock()
}

func TestLockBucketContextProceeds(t *testing.T) {
	r := NewRatelimiter()
	b, err := r.LockBucketContext(context.Background(), "fresh-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Remaining != 0 {
		t.Fatalf("expected Remaining=0 after lock, got %d", b.Remaining)
	}
	b.Release(nil)
}

func TestRateLimiterRemoveAndPrune(t *testing.T) {
	r := NewRatelimiter()
	r.GetBucket("remove-me")
	r.RemoveBucket("remove-me")
	r.bucketsMuCheckRemoved(t, "remove-me")

	// Fill the map well past the prune threshold with stale buckets;
	// pruning runs opportunistically once the map reaches 512 entries.
	for i := 0; i < 1000; i++ {
		r.GetBucket("expired-" + strconv.Itoa(i))
	}
	if len(r.buckets) >= 1000 {
		t.Fatalf("expected opportunistic pruning to cap bucket growth, got %d", len(r.buckets))
	}

	// The most recently created bucket must survive pruning.
	if _, ok := r.buckets["expired-999"]; !ok {
		t.Fatal("the most recent bucket must survive pruning")
	}
}

// helper keeps the test independent from sync internals of RateLimiter
func (r *RateLimiter) bucketsMuCheckRemoved(t *testing.T, key string) {
	t.Helper()
	r.Lock()
	defer r.Unlock()
	if _, ok := r.buckets[key]; ok {
		t.Fatalf("bucket %q should have been removed", key)
	}
}

func TestShardIDForGuild(t *testing.T) {
	// Snowflake 1300000000000000000 has (id>>22) == 310058; 310058 % 4 == 2.
	cases := []struct {
		id    string
		count int
		want  int
	}{
		// (id>>22) == 2 for 2<<22+2.
		{"8388610", 4, 2},
		// (id>>22) == 309936103820 for this snowflake; ...820 % 4 == 0.
		{"1300000000000000000", 4, 0},
		{"1300000000000000000", 1, 0},
		{"0", 16, 0},
	}
	for _, c := range cases {
		got, err := ShardIDForGuild(c.id, c.count)
		if err != nil {
			t.Fatalf("ShardIDForGuild(%q, %d): %v", c.id, c.count, err)
		}
		if got != c.want {
			t.Fatalf("ShardIDForGuild(%q, %d) = %d, want %d", c.id, c.count, got, c.want)
		}
	}

	if _, err := ShardIDForGuild("123", 0); err != ErrWSShardBounds {
		t.Fatalf("expected ErrWSShardBounds for zero shard count, got %v", err)
	}
	if _, err := ShardIDForGuild("not-a-snowflake", 2); err == nil {
		t.Fatal("expected error for non-numeric guild ID")
	}
}

func TestParseSnowflakeInt64(t *testing.T) {
	n, err := ParseSnowflakeInt64("1300000000000000000")
	if err != nil || n != 1300000000000000000 {
		t.Fatalf("got (%d, %v)", n, err)
	}
	for _, bad := range []string{"", "12a3", "-5", "99999999999999999999999"} {
		if _, err := ParseSnowflakeInt64(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestIdentifyDelayForConcurrency(t *testing.T) {
	if got := identifyDelayForConcurrency(0); got != DefaultIdentifyDelay {
		t.Fatalf("concurrency 0 should behave like 1, got %v", got)
	}
	if got := identifyDelayForConcurrency(1); got != 5*time.Second {
		t.Fatalf("got %v", got)
	}
	if got := identifyDelayForConcurrency(16); got != 5*time.Second/16 {
		t.Fatalf("got %v", got)
	}
}

func TestShardManagerFanOut(t *testing.T) {
	m := NewShardManager(ShardingConfig{Token: "Bot test", ShardCount: 3})

	// Register before any session exists.
	called := 0
	m.AddHandler(func(s *Session, e *Connect) { called++ })

	s0, _ := New("Bot test")
	s1, _ := New("Bot test")
	s0.SyncEvents = true // dispatch synchronously so the test can assert
	// Attach sessions the same way Open does internally.
	m.mu.Lock()
	m.addSessionLocked(s0)
	m.addSessionLocked(s1)
	m.mu.Unlock()

	// Register after sessions exist.
	m.AddHandler(func(s *Session, e *Disconnect) {})

	for _, s := range m.Sessions {
		s.Lock()
		total := 0
		for _, hs := range s.handlers {
			total += len(hs)
		}
		s.Unlock()
		if total != 2 {
			t.Fatalf("shard %d: %d handlers registered, want 2", s.ShardID, total)
		}
	}

	// The pre-registered handler must actually fire on a session.
	s0.handleEvent(connectEventType, &Connect{})
	if called != 1 {
		t.Fatalf("handler fired %d times, want 1", called)
	}
}

// redirectHost is the httptest server host used to redirect Discord REST
// calls in tests.
var redirectHost string

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGuildBulkBan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/bulk-ban") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"banned_users":["1","2"],"failed_user_ids":["3"]}`))
	}))
	defer srv.Close()
	redirectHost = srv.Listener.Addr().String()

	s, _ := New("Bot test-token")
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = redirectHost
		return http.DefaultTransport.RoundTrip(r)
	})}
	resp, err := s.GuildBulkBan("guild-id", []string{"1", "2", "3"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.BannedUsers) != 2 || resp.BannedUsers[0] != "1" {
		t.Fatalf("unexpected banned users: %v", resp.BannedUsers)
	}
	if len(resp.FailedUserIDs) != 1 || resp.FailedUserIDs[0] != "3" {
		t.Fatalf("unexpected failed users: %v", resp.FailedUserIDs)
	}
}
