package discordgo

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// Guards the REST fast path: the first attempt must run on the original
// request, and every retry (5xx and 429 alike) must resend the full body.
func Test429RetryPreservesBody(t *testing.T) {
	s, _ := New("")
	var attempts atomic.Int32
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n := attempts.Add(1)
		gotBody, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil || string(gotBody) != `{"value":1}` {
			t.Errorf("attempt %d body=%q err=%v", n, gotBody, err)
		}
		status := http.StatusOK
		if n == 1 {
			status = http.StatusTooManyRequests
		}
		var body io.ReadCloser = http.NoBody
		contentLength := int64(0)
		if status == http.StatusTooManyRequests {
			// A real Discord 429 carries a JSON body; the library decodes it.
			body = io.NopCloser(strings.NewReader(`{"retry_after":0}`))
			contentLength = int64(len(`{"retry_after":0}`))
		}
		return &http.Response{
			StatusCode:    status,
			Status:        "200 OK",
			Header:        http.Header{},
			Body:          body,
			ContentLength: contentLength,
			Request:       r,
		}, nil
	})}

	if _, err := s.Request("POST", "https://example.com", map[string]int{"value": 1}, WithRetryOnRatelimit(true)); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2 (one 429 + one successful retry)", got)
	}
}

func TestReadBodyContentLengthMismatch(t *testing.T) {
	// Declared longer than the actual body: return the short body, no error.
	resp := &http.Response{
		Body:          io.NopCloser(strings.NewReader("hello")),
		ContentLength: 10,
	}
	b, err := readBody(resp)
	if err != nil || string(b) != "hello" {
		t.Fatalf("short body: got %q, err=%v", b, err)
	}

	// Declared shorter than the actual body: drain and return the excess.
	resp = &http.Response{
		Body:          io.NopCloser(strings.NewReader("hello world")),
		ContentLength: 5,
	}
	b, err = readBody(resp)
	if err != nil || string(b) != "hello world" {
		t.Fatalf("long body: got %q, err=%v", b, err)
	}

	// Unknown length (chunked): fall back to io.ReadAll semantics.
	resp = &http.Response{
		Body:          io.NopCloser(strings.NewReader("chunked body")),
		ContentLength: -1,
	}
	b, err = readBody(resp)
	if err != nil || string(b) != "chunked body" {
		t.Fatalf("unknown length: got %q, err=%v", b, err)
	}
}
