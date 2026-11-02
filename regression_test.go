package discordgo

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSnowflakeUint64Bounds(t *testing.T) {
	if n, err := ParseSnowflakeInt64("18446744073709551615"); err != nil || n != math.MaxUint64 {
		t.Fatalf("maximum uint64: got %d, %v", n, err)
	}
	for _, id := range []string{"18446744073709551616", "18446744073709551619", "+1"} {
		if _, err := ParseSnowflakeInt64(id); !errors.Is(err, ErrJSONUnmarshal) {
			t.Errorf("%q: expected ErrJSONUnmarshal, got %v", id, err)
		}
	}
}

func TestRequestTimeoutIncludesBucketWait(t *testing.T) {
	s, _ := New("")
	b := s.Ratelimiter.GetBucket("timeout")
	b.Remaining = 0
	b.reset = time.Now().Add(200 * time.Millisecond)
	calls := 0
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
	})}
	_, err := s.RequestWithBucketID("GET", "https://example.com", nil, "timeout", WithTimeout(20*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatalf("expected timeout before transport; err=%v, calls=%d", err, calls)
	}
	if !b.TryLock() {
		t.Fatal("cancelled wait left bucket locked")
	}
	b.Unlock()
}

func TestLockBucketContextCancelsMutexWait(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("busy")
	b.Lock()
	defer b.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		locked, err := r.LockBucketObjectContext(ctx, b)
		if locked != nil {
			_ = locked.Release(nil)
		}
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt mutex wait")
	}
}

func TestRequestRetriesShareDeadline(t *testing.T) {
	for _, status := range []int{500, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, _ := New("")
			var deadline time.Time
			attempts := 0
			s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				got, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("request has no deadline")
				}
				if attempts == 1 {
					deadline = got
					var body io.ReadCloser = http.NoBody
					if status == 429 {
						body = io.NopCloser(strings.NewReader(`{"retry_after":0}`))
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: body}, nil
				}
				if !got.Equal(deadline) {
					t.Errorf("retry reset deadline: first=%v retry=%v", deadline, got)
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			})}
			_, err := s.Request("GET", "https://example.com", nil, WithTimeout(40*time.Millisecond))
			if !errors.Is(err, context.DeadlineExceeded) || attempts != 2 {
				t.Fatalf("expected timeout on retry; err=%v attempts=%d", err, attempts)
			}
		})
	}
}

func TestRequestRetryPreservesBodyAndOptions(t *testing.T) {
	s, _ := New("")
	attempts, optionCalls := 0, 0
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil || string(body) != `{"value":1}` {
			t.Errorf("attempt %d body=%q err=%v", attempts, body, err)
		}
		if r.URL.Query().Get("key") != "value" || r.Header.Get("X-Test") != "yes" {
			t.Errorf("attempt %d lost query/header", attempts)
		}
		status := 200
		if attempts == 1 {
			status = 500
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: http.NoBody}, nil
	})}
	_, err := s.Request("POST", "https://example.com", map[string]int{"value": 1},
		WithQuery("key", "value"), WithHeader("X-Test", "yes"),
		func(cfg *RequestConfig) { optionCalls++ })
	if err != nil || attempts != 2 || optionCalls != 1 {
		t.Fatalf("err=%v attempts=%d option calls=%d", err, attempts, optionCalls)
	}
}

func TestDispatchWorkerStopsAfterActiveHandler(t *testing.T) {
	workers := func() int {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		return bytes.Count(buf[:n], []byte("startDispatchWorkers.func"))
	}
	before := workers()
	s, _ := New("")
	s.DispatchWorkers = 1
	s.startDispatchWorkers()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	s.handlersMu.RLock()
	s.submitDispatch(func() { close(entered); <-release; close(done) })
	s.handlersMu.RUnlock()
	<-entered
	s.stopDispatchWorkers()
	close(release)
	<-done
	deadline := time.Now().Add(time.Second)
	for workers() > before {
		if time.Now().After(deadline) {
			t.Fatal("worker remained blocked after shutdown")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestShardForGuildIDUsesConfiguredShardCount(t *testing.T) {
	sessions := []*Session{{ShardID: 0}, {ShardID: 1}, {ShardID: 2}}
	m := NewShardManager(ShardingConfig{ShardCount: 3})
	m.Sessions = sessions
	if got := m.ShardForGuildID("4194304"); got != sessions[1] {
		t.Fatalf("expected shard 1, got %v", got)
	}
	if allocs := testing.AllocsPerRun(100, func() { m.ShardForGuildID("4194304") }); allocs != 0 {
		t.Fatalf("shard lookup allocates: %v", allocs)
	}
}
