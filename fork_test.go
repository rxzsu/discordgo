package discordgo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
