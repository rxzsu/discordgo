package discordgo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRESTMetricsIncludeRetriesAndRateLimits(t *testing.T) {
	s, _ := New("")
	attempt := 0
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempt++
		status, body := 200, ""
		switch attempt {
		case 1:
			status = 500
		case 2:
			status, body = 429, `{"retry_after":0.001}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	var records []RESTRequestMetrics
	s.RequestObserver = func(m RESTRequestMetrics) {
		records = append(records, m)
		b := s.Ratelimiter.GetBucket("metrics")
		if !b.TryLock() {
			t.Error("observer called while bucket is locked")
		} else {
			b.Unlock()
		}
	}
	if _, err := s.RequestWithBucketID("POST", "https://example.com", nil, "metrics"); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected one completion, got %d", len(records))
	}
	m := records[0]
	if m.Method != "POST" || m.Attempts != 3 || m.Retries != 2 || m.RateLimits != 1 || m.StatusCode != 200 || m.Error != nil || m.Duration <= 0 {
		t.Fatalf("unexpected metrics: %+v", m)
	}
}

func TestRESTMetricsReportBucketCancellation(t *testing.T) {
	s, _ := New("")
	b := s.Ratelimiter.GetBucket("wait")
	b.Remaining, b.reset = 0, time.Now().Add(time.Second)
	var record RESTRequestMetrics
	called := 0
	s.RequestObserver = func(m RESTRequestMetrics) { record = m; called++ }
	_, err := s.RequestWithBucketID("GET", "https://example.com", nil, "wait", WithTimeout(10*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(record.Error, context.DeadlineExceeded) {
		t.Fatalf("request error=%v metric error=%v", err, record.Error)
	}
	if called != 1 || record.Attempts != 0 || record.StatusCode != 0 || record.Retries != 0 || record.Duration < 10*time.Millisecond {
		t.Fatalf("unexpected cancellation metrics: %+v called=%d", record, called)
	}
}

func TestRESTMetricsReportFailures(t *testing.T) {
	transportErr := errors.New("transport failed")
	for _, kind := range []string{"transport", "HTTP", "invalid URL", "rate limit"} {
		t.Run(kind, func(t *testing.T) {
			s, _ := New("")
			s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if kind == "transport" {
					return nil, transportErr
				}
				status, body := 400, `{"message":"bad request","code":50035}`
				if kind == "rate limit" {
					status, body = 429, `{"retry_after":1}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			var record RESTRequestMetrics
			called := 0
			s.RequestObserver = func(m RESTRequestMetrics) { record = m; called++ }
			url := "https://example.com"
			if kind == "invalid URL" {
				url = "://invalid"
			}
			_, err := s.Request("GET", url, nil, WithRetryOnRatelimit(false))
			if err == nil || record.Error != err || called != 1 || record.Retries != 0 {
				t.Fatalf("request error=%v metrics=%+v called=%d", err, record, called)
			}
			switch kind {
			case "invalid URL":
				if record.Attempts != 0 || record.StatusCode != 0 {
					t.Fatalf("invalid URL reached transport: %+v", record)
				}
			case "transport":
				if record.Attempts != 1 || record.StatusCode != 0 || !errors.Is(record.Error, transportErr) {
					t.Fatalf("unexpected transport metrics: %+v", record)
				}
			case "HTTP":
				var restErr *RESTError
				if record.StatusCode != 400 || !errors.As(record.Error, &restErr) {
					t.Fatalf("unexpected HTTP metrics: %+v", record)
				}
			case "rate limit":
				if record.StatusCode != 429 || record.RateLimits != 1 || record.Attempts != 1 {
					t.Fatalf("unexpected rate limit metrics: %+v", record)
				}
			}
		})
	}
}

func TestRESTMetricsConcurrentRequests(t *testing.T) {
	s, _ := New("")
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Header: http.Header{}, Body: http.NoBody}, nil
	})}
	var mu sync.Mutex
	records := 0
	s.RequestObserver = func(m RESTRequestMetrics) {
		if m.Attempts != 1 || m.StatusCode != 204 || m.Error != nil {
			t.Errorf("unexpected metrics: %+v", m)
		}
		mu.Lock()
		records++
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if _, err := s.Request("GET", "https://example.com", nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if records != 160 {
		t.Fatalf("completion count=%d", records)
	}
}

func TestRESTMetricsWithLockedBucket(t *testing.T) {
	s, _ := New("")
	b := s.Ratelimiter.LockBucket("locked")
	called := false
	s.RequestObserver = func(m RESTRequestMetrics) {
		called = true
		if !b.TryLock() {
			t.Error("invalid request left bucket locked during callback")
		} else {
			b.Unlock()
		}
	}
	_, err := s.RequestWithLockedBucket("GET", "://invalid", "", nil, b, 0)
	if err == nil || !called {
		t.Fatal("missing invalid request completion")
	}
}

func TestRESTMetricsReflectConfiguredMethod(t *testing.T) {
	for _, method := range []string{"GET", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			s, _ := New("")
			s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != method {
					t.Errorf("transport method=%s", r.Method)
				}
				return &http.Response{StatusCode: 204, Header: http.Header{}, Body: http.NoBody}, nil
			})}
			s.RequestObserver = func(m RESTRequestMetrics) {
				if m.Method != method {
					t.Errorf("metric method=%s, expected %s", m.Method, method)
				}
			}
			var options []RequestOption
			if method == "PATCH" {
				options = append(options, func(cfg *RequestConfig) { cfg.Request.Method = method })
			}
			// net/http defaults an empty method to GET.
			if _, err := s.Request("", "https://example.com", nil, options...); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func BenchmarkRequestObserver(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		b.Run(name, func(b *testing.B) {
			s, _ := New("Bot benchmark-token")
			s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: http.NoBody}, nil
			})}
			called := 0
			if enabled {
				s.RequestObserver = func(RESTRequestMetrics) { called++ }
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.RequestWithBucketID("GET", "https://discord.com/api/v9/users/123", nil, ""); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if enabled && called != b.N {
				b.Fatalf("callbacks=%d requests=%d", called, b.N)
			}
		})
	}
}
