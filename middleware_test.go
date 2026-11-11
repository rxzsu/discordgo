package discordgo

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mwDispatch drives an event through the real dispatch path of a session
// without a gateway. SyncEvents makes handlers run synchronously, mirroring
// the typed_test.go pattern (own helper to stay independent of that file).
func mwDispatch(t *testing.T, s *Session, eventType string, payload interface{}) {
	t.Helper()
	s.SyncEvents = true
	s.handleEvent(eventType, payload)
}

// captureLogger swaps the package logger for a capturing one and returns a
// restore function plus a pointer to the collected formatted lines.
func captureLogger() (restore func(), lines *[]string) {
	var mu sync.Mutex
	collected := &[]string{}
	prev := Logger
	Logger = func(msgL int, _ int, format string, a ...interface{}) {
		mu.Lock()
		defer mu.Unlock()
		*collected = append(*collected, fmt.Sprintf(format, a...))
	}
	return func() { Logger = prev }, collected
}

func TestUseTypedOrdering(t *testing.T) {
	var order []string
	mk := func(name string) TypedMiddleware[MessageCreate] {
		return func(next TypedHandlerFunc[MessageCreate]) TypedHandlerFunc[MessageCreate] {
			return func(s *Session, e *MessageCreate) {
				order = append(order, name+"-pre")
				next(s, e)
				order = append(order, name+"-post")
			}
		}
	}
	h := func(*Session, *MessageCreate) { order = append(order, "h") }

	wrapped := UseTyped(h, mk("m1"), mk("m2"), mk("m3"))
	wrapped(nil, &MessageCreate{})

	want := []string{"m1-pre", "m2-pre", "m3-pre", "h", "m3-post", "m2-post", "m1-post"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected order: got %v, want %v", order, want)
	}
}

func TestUseTypedNilMiddlewareSkipped(t *testing.T) {
	called := false
	h := func(*Session, *MessageCreate) { called = true }
	wrapped := UseTyped(h, nil, nil)
	wrapped(nil, &MessageCreate{})
	if !called {
		t.Fatal("handler must run when all middlewares are nil")
	}
	if UseTyped[MessageCreate](h) == nil {
		t.Fatal("UseTyped without middlewares must return the handler")
	}
}

func TestChainTypedEarlyExit(t *testing.T) {
	hits := 0
	handler := TypedHandlerFunc[MessageCreate](func(*Session, *MessageCreate) { hits++ })

	// gate stops the chain when allow is false; later middleware and the
	// handler must never run.
	gate := func(allow bool) TypedMiddleware[MessageCreate] {
		return func(next TypedHandlerFunc[MessageCreate]) TypedHandlerFunc[MessageCreate] {
			return func(s *Session, e *MessageCreate) {
				if allow {
					next(s, e)
				}
			}
		}
	}
	passthrough := func(next TypedHandlerFunc[MessageCreate]) TypedHandlerFunc[MessageCreate] {
		return next
	}

	chain := ChainTyped(passthrough, gate(false), passthrough)
	chain(handler)(nil, &MessageCreate{})
	if hits != 0 {
		t.Fatalf("handler ran despite gated chain: %d hits", hits)
	}

	chain = ChainTyped(passthrough, gate(true), passthrough)
	chain(handler)(nil, &MessageCreate{})
	if hits != 1 {
		t.Fatalf("expected exactly 1 hit through open chain, got %d", hits)
	}
}

func TestAddHandlerTypedWithDispatch(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	var passed, dropped int
	mw := FilterMessageCreateEvents(ScopeFilter{ChannelID: "c-ok"})
	remove := AddHandlerTypedWith(s, func(_ *Session, m *MessageCreate) { passed++ }, mw)
	defer remove()

	s.AddHandler(func(_ *Session, m *MessageCreate) { dropped++ }) // unfiltered control

	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1", ChannelID: "c-ok"}})
	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m2", ChannelID: "c-no"}})

	if passed != 1 || dropped != 2 {
		t.Fatalf("expected 1 passed / 2 dispatched, got %d / %d", passed, dropped)
	}
}

func TestAddHandlerOnceTypedWithDispatch(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := AddHandlerOnceTypedWith(s, func(*Session, *MessageCreate) { calls++ },
		RecoverTyped[MessageCreate]())
	defer remove()

	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1"}})
	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m2"}})
	if calls != 1 {
		t.Fatalf("expected once handler with middleware to fire once, fired %d", calls)
	}
}

func TestRecoverTypedDefault(t *testing.T) {
	restore, lines := captureLogger()
	defer restore()

	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	var after int
	remove := AddHandlerTypedWith(s,
		func(*Session, *MessageCreate) { panic("boom") },
		RecoverTyped[MessageCreate](),
	)
	// A second handler proves the dispatch survives the recovered panic.
	AddHandlerTyped(s, func(*Session, *MessageCreate) { after++ })
	defer remove()

	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1"}})
	if after != 1 {
		t.Fatalf("dispatch did not survive recovered panic, after=%d", after)
	}
	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "panic recovered") || !strings.Contains(joined, "boom") {
		t.Fatalf("expected recovered panic in log, got: %s", joined)
	}
}

func TestRecoverTypedWithReporter(t *testing.T) {
	var gotSession *Session
	var gotValue interface{}
	reporter := func(s *Session, e *MessageCreate, r interface{}) {
		gotSession = s
		gotValue = r
	}
	mw := RecoverTypedWith[MessageCreate](reporter)
	h := mw(func(*Session, *MessageCreate) { panic(errors.New("kaboom")) })
	h(nil, &MessageCreate{})
	if gotValue == nil || gotValue.(error).Error() != "kaboom" {
		t.Fatalf("reporter did not receive the panic value: %v", gotValue)
	}
	_ = gotSession // nil session is fine; handlers may get a real one via dispatch

	// No panic escapes when the handler is calm.
	calm := mw(func(*Session, *MessageCreate) {})
	calm(nil, &MessageCreate{})
}

func TestLogTypedMessages(t *testing.T) {
	restore, lines := captureLogger()
	defer restore()

	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	// The default session level is LogError; logging middleware messages
	// (LogDebug/LogWarning) only appear from LogDebug upwards.
	s.LogLevel = LogDebug

	remove := AddHandlerTypedWith(s, func(*Session, *MessageCreate) { time.Sleep(2 * time.Millisecond) },
		LogTyped[MessageCreate]("myHandler"))
	defer remove()

	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1"}})

	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "myHandler") {
		t.Fatalf("expected handler name in log, got: %s", joined)
	}

	// Slow path: a nanosecond threshold escalates to LogWarning.
	mw := LogTypedWith[MessageCreate](TypedLogConfig{Name: "slowHandler", Slow: time.Nanosecond})
	mw(func(*Session, *MessageCreate) { time.Sleep(2 * time.Millisecond) })(s, &MessageCreate{})
	joined = strings.Join(*lines, "\n")
	if !strings.Contains(joined, "slowHandler") {
		t.Fatalf("expected slow handler in log, got: %s", joined)
	}
}

func TestFilterCombinators(t *testing.T) {
	get := func(e *MessageCreate) string { return e.Message.ChannelID }
	tests := []struct {
		name    string
		mw      TypedMiddleware[MessageCreate]
		event   *MessageCreate
		wantRun bool
	}{
		{"eq-match", FilterEq[MessageCreate](get, "c1"), &MessageCreate{Message: &Message{ChannelID: "c1"}}, true},
		{"eq-mismatch", FilterEq[MessageCreate](get, "c2"), &MessageCreate{Message: &Message{ChannelID: "c1"}}, false},
		{"oneof-match", FilterOneOf[MessageCreate](get, "a", "c1"), &MessageCreate{Message: &Message{ChannelID: "c1"}}, true},
		{"oneof-miss", FilterOneOf[MessageCreate](get, "a", "b"), &MessageCreate{Message: &Message{ChannelID: "c1"}}, false},
		{"not-drops-match", FilterNot[MessageCreate](FilterEq[MessageCreate](get, "c1")), &MessageCreate{Message: &Message{ChannelID: "c1"}}, false},
		{"not-passes-miss", FilterNot[MessageCreate](FilterEq[MessageCreate](get, "c1")), &MessageCreate{Message: &Message{ChannelID: "zz"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			tc.mw(func(*Session, *MessageCreate) { ran = true })(nil, tc.event)
			if ran != tc.wantRun {
				t.Fatalf("ran = %v, want %v", ran, tc.wantRun)
			}
		})
	}
}

func TestFilterMessageCreateEvents(t *testing.T) {
	tests := []struct {
		name   string
		filter ScopeFilter
		event  *MessageCreate
		want   bool
	}{
		{
			name:   "wildcard passes",
			filter: ScopeFilter{},
			event:  &MessageCreate{Message: &Message{GuildID: "g1", ChannelID: "c1", Author: &User{ID: "u1"}}},
			want:   true,
		},
		{
			name:   "guild match",
			filter: ScopeFilter{GuildID: "g1"},
			event:  &MessageCreate{Message: &Message{GuildID: "g1"}},
			want:   true,
		},
		{
			name:   "guild mismatch",
			filter: ScopeFilter{GuildID: "g9"},
			event:  &MessageCreate{Message: &Message{GuildID: "g1"}},
			want:   false,
		},
		{
			name:   "channel+author match",
			filter: ScopeFilter{ChannelID: "c1", AuthorID: "u1"},
			event:  &MessageCreate{Message: &Message{ChannelID: "c1", Author: &User{ID: "u1"}}},
			want:   true,
		},
		{
			name:   "author mismatch",
			filter: ScopeFilter{AuthorID: "u1"},
			event:  &MessageCreate{Message: &Message{Author: &User{ID: "u2"}}},
			want:   false,
		},
		{
			name:   "nil author dropped when scoped",
			filter: ScopeFilter{AuthorID: "u1"},
			event:  &MessageCreate{Message: &Message{}},
			want:   false,
		},
		{
			name:   "nil message dropped when scoped",
			filter: ScopeFilter{GuildID: "g1"},
			event:  &MessageCreate{},
			want:   false,
		},
		{
			name:   "nil message passes wildcard",
			filter: ScopeFilter{},
			event:  &MessageCreate{},
			want:   true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			mw := FilterMessageCreateEvents(tc.filter)
			mw(func(*Session, *MessageCreate) { ran = true })(nil, tc.event)
			if ran != tc.want {
				t.Fatalf("ran = %v, want %v", ran, tc.want)
			}
		})
	}
}

func TestFilterMessageUpdateDeleteEvents(t *testing.T) {
	ranUpd, ranDel := false, false
	mwUpd := FilterMessageUpdateEvents(ScopeFilter{GuildID: "g1"})
	mwUpd(func(*Session, *MessageUpdate) { ranUpd = true })(nil,
		&MessageUpdate{Message: &Message{GuildID: "g1"}})

	mwDel := FilterMessageDeleteEvents(ScopeFilter{GuildID: "g1"})
	mwDel(func(*Session, *MessageDelete) { ranDel = true })(nil,
		&MessageDelete{Message: &Message{ID: "m1", GuildID: "g1"}}) // partial message, no author

	if !ranUpd || !ranDel {
		t.Fatalf("update ran=%v, delete ran=%v", ranUpd, ranDel)
	}
}

func TestFilterInteractionEvents(t *testing.T) {
	tests := []struct {
		name   string
		filter ScopeFilter
		event  *InteractionCreate
		want   bool
	}{
		{
			name:   "guild member match",
			filter: ScopeFilter{GuildID: "g1", AuthorID: "u1"},
			event:  &InteractionCreate{Interaction: &Interaction{GuildID: "g1", Member: &Member{User: &User{ID: "u1"}}}},
			want:   true,
		},
		{
			name:   "dm user match",
			filter: ScopeFilter{AuthorID: "u2"},
			event:  &InteractionCreate{Interaction: &Interaction{User: &User{ID: "u2"}}},
			want:   true,
		},
		{
			name:   "channel mismatch",
			filter: ScopeFilter{ChannelID: "c1"},
			event:  &InteractionCreate{Interaction: &Interaction{ChannelID: "c9"}},
			want:   false,
		},
		{
			name:   "nil interaction dropped when scoped",
			filter: ScopeFilter{GuildID: "g1"},
			event:  &InteractionCreate{},
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			mw := FilterInteractionEvents(tc.filter)
			mw(func(*Session, *InteractionCreate) { ran = true })(nil, tc.event)
			if ran != tc.want {
				t.Fatalf("ran = %v, want %v", ran, tc.want)
			}
		})
	}
}

func TestDedupTyped(t *testing.T) {
	key := func(e *MessageCreate) string { return e.Message.ID }

	ran := 0
	next := TypedHandlerFunc[MessageCreate](func(*Session, *MessageCreate) { ran++ })

	dedup := DedupTyped[MessageCreate](key, 40*time.Millisecond)
	wrapped := dedup(next)

	mk := func(id string) *MessageCreate { return &MessageCreate{Message: &Message{ID: id}} }

	wrapped(nil, mk("m1")) // first -> runs
	wrapped(nil, mk("m1")) // duplicate -> dropped
	wrapped(nil, mk("m2")) // other key -> runs
	if ran != 2 {
		t.Fatalf("expected 2 runs after dedup, got %d", ran)
	}

	time.Sleep(50 * time.Millisecond) // ttl expired
	wrapped(nil, mk("m1"))
	if ran != 3 {
		t.Fatalf("expected event to pass after ttl expiry, ran=%d", ran)
	}

	// Disabled variants pass everything through.
	ran = 0
	passAll := DedupTyped[MessageCreate](nil, time.Minute)
	w2 := passAll(next)
	w2(nil, mk("x"))
	w2(nil, mk("x"))
	if ran != 2 {
		t.Fatalf("nil key must disable dedup, ran=%d", ran)
	}
	ran = 0
	passAll = DedupTyped[MessageCreate](key, 0)
	passAll(next)(nil, mk("x"))
	passAll(next)(nil, mk("x"))
	if ran != 2 {
		t.Fatalf("ttl <= 0 must disable dedup, ran=%d", ran)
	}
}

func TestDedupTypedConcurrent(t *testing.T) {
	dedup := DedupTyped[MessageCreate](func(e *MessageCreate) string { return e.Message.ID }, time.Second)
	var runs atomic.Int64
	wrapped := dedup(TypedHandlerFunc[MessageCreate](func(*Session, *MessageCreate) { runs.Add(1) }))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wrapped(nil, &MessageCreate{Message: &Message{ID: "same"}})
		}()
	}
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatalf("expected exactly 1 pass for 32 concurrent duplicates, got %d", runs.Load())
	}
}

func TestThrottleTyped(t *testing.T) {
	var drops atomic.Int64
	cfg := ThrottleConfig[MessageCreate]{
		Events: 100, Per: time.Second, Burst: 3,
		OnDrop: func(_ *Session, e *MessageCreate) { drops.Add(1) },
	}
	throttle := ThrottleTyped[MessageCreate](func(e *MessageCreate) string { return e.Message.Author.ID }, cfg)
	wrapped := throttle(func(*Session, *MessageCreate) {})

	mk := func() *MessageCreate { return &MessageCreate{Message: &Message{Author: &User{ID: "u1"}}} }

	// Burst: 3 pass, everything beyond is dropped.
	for i := 0; i < 5; i++ {
		wrapped(nil, mk())
	}
	if d := drops.Load(); d != 2 {
		t.Fatalf("expected 2 drops over burst, got %d", d)
	}

	// Refill: after 150 ms at 100 events/s the bucket is full again.
	time.Sleep(150 * time.Millisecond)
	drops.Store(0)
	for i := 0; i < 3; i++ {
		wrapped(nil, mk())
	}
	if d := drops.Load(); d != 0 {
		t.Fatalf("expected refill to allow 3 events, got %d drops", d)
	}

	// Disabled variants pass everything through.
	passthrough := ThrottleTyped[MessageCreate](nil, cfg)
	ran := false
	passthrough(func(*Session, *MessageCreate) { ran = true })(nil, mk())
	if !ran {
		t.Fatal("nil key must disable throttling")
	}
	badCfg := cfg
	badCfg.Events = 0
	passthrough = ThrottleTyped[MessageCreate](func(e *MessageCreate) string { return "" }, badCfg)
	ran = false
	passthrough(func(*Session, *MessageCreate) { ran = true })(nil, mk())
	if !ran {
		t.Fatal("invalid rate must disable throttling")
	}
}

func TestThrottleTypedIndependentKeys(t *testing.T) {
	cfg := ThrottleConfig[MessageCreate]{Events: 1, Per: time.Minute, Burst: 1}
	throttle := ThrottleTyped[MessageCreate](func(e *MessageCreate) string { return e.Message.Author.ID }, cfg)

	mk := func(id string) *MessageCreate { return &MessageCreate{Message: &Message{Author: &User{ID: id}}} }

	// Each user has its own bucket: u1's second event is dropped, u2's
	// first event still passes.
	ran1, ran2 := false, false
	u1 := throttle(func(*Session, *MessageCreate) { ran1 = true })
	u2 := throttle(func(*Session, *MessageCreate) { ran2 = true })

	u1(nil, mk("u1")) // consumes u1's only token
	ran1 = false
	u1(nil, mk("u1")) // dropped
	u2(nil, mk("u2")) // own bucket, passes
	if ran1 {
		t.Fatal("second event of u1 must be dropped")
	}
	if !ran2 {
		t.Fatal("u2 must have an independent bucket")
	}
}

func TestThrottleTypedConcurrent(t *testing.T) {
	cfg := ThrottleConfig[MessageCreate]{Events: 1, Per: time.Minute, Burst: 10}
	throttle := ThrottleTyped[MessageCreate](func(e *MessageCreate) string { return "same" }, cfg)
	var runs atomic.Int64
	wrapped := throttle(TypedHandlerFunc[MessageCreate](func(*Session, *MessageCreate) { runs.Add(1) }))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				wrapped(nil, &MessageCreate{Message: &Message{ID: "x"}})
			}
		}()
	}
	wg.Wait()

	// Burst is 10 and Per is a minute, so at most 10 + a negligible refill
	// may pass; nothing above 11 can be legitimate.
	if got := runs.Load(); got > 11 {
		t.Fatalf("throttle let through %d events, expected <= 11", got)
	}
	if got := runs.Load(); got < 1 {
		t.Fatalf("throttle dropped everything, expected at least 1 pass")
	}
}

func TestTypedPipeline(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	var trace []string
	p := NewTypedPipeline[MessageCreate](
		RecoverTyped[MessageCreate](),
	).Use(
		func(next TypedHandlerFunc[MessageCreate]) TypedHandlerFunc[MessageCreate] {
			return func(s *Session, e *MessageCreate) {
				trace = append(trace, "mw")
				next(s, e)
			}
		},
	)

	remove := p.Register(s, func(*Session, *MessageCreate) { trace = append(trace, "h") })

	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1"}})
	if strings.Join(trace, ",") != "mw,h" {
		t.Fatalf("unexpected pipeline trace: %v", trace)
	}
	remove() // so the next dispatches below see only their own handler

	// The accumulated middleware also wraps handlers directly.
	trace = nil
	p.Handler(func(*Session, *MessageCreate) { trace = append(trace, "h2") })(nil, &MessageCreate{})
	if strings.Join(trace, ",") != "mw,h2" {
		t.Fatalf("unexpected Handler() trace: %v", trace)
	}

	// Middleware() composes with AddHandlerTypedWith.
	trace = nil
	remove2 := AddHandlerTypedWith(s, func(*Session, *MessageCreate) { trace = append(trace, "h3") }, p.Middleware())
	defer remove2()
	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m2"}})
	if strings.Join(trace, ",") != "mw,h3" {
		t.Fatalf("unexpected Middleware() trace: %v", trace)
	}
}

func TestTypedPipelineRegisterOnce(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := NewTypedPipeline[MessageCreate](FilterTyped[MessageCreate](func(*Session, *MessageCreate) bool { return true }))
	remove := p.RegisterOnce(s, func(*Session, *MessageCreate) { calls++ })
	defer remove()
	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1"}})
	mwDispatch(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m2"}})
	if calls != 1 {
		t.Fatalf("expected RegisterOnce to fire once, fired %d", calls)
	}
}
