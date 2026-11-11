package discordgo

import (
	"context"
	"testing"
	"time"
)

// dispatchEvent drives an event through the real dispatch path
// (handleEvent: internal handlers, interface{} handlers, typed handlers) of
// a session created without a gateway. SyncEvents makes the handlers run
// synchronously, so no sleeps are needed.
func dispatchEvent(t *testing.T, s *Session, eventType string, payload interface{}) {
	t.Helper()
	s.SyncEvents = true
	s.handleEvent(eventType, payload)
}

func TestAddHandlerTypedDispatch(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   interface{}
		// register installs the typed handler under test and returns a
		// channel receiving whatever the handler got.
		register func(s *Session) <-chan interface{}
		verify   func(t *testing.T, got interface{})
	}{
		{
			name:      "MessageCreate",
			eventType: "MESSAGE_CREATE",
			payload:   &MessageCreate{Message: &Message{ID: "m1", ChannelID: "c1", Content: "hello", Author: &User{ID: "u1", Bot: false}}},
			register: func(s *Session) <-chan interface{} {
				ch := make(chan interface{}, 1)
				AddHandlerTyped(s, func(_ *Session, m *MessageCreate) { ch <- m })
				return ch
			},
			verify: func(t *testing.T, got interface{}) {
				t.Helper()
				m, ok := got.(*MessageCreate)
				if !ok {
					t.Fatalf("expected *MessageCreate, got %T", got)
				}
				if m.ID != "m1" || m.Content != "hello" {
					t.Fatalf("unexpected event: %+v", m)
				}
			},
		},
		{
			name:      "GuildCreate",
			eventType: "GUILD_CREATE",
			payload:   &GuildCreate{Guild: &Guild{ID: "g1", Name: "guild"}},
			register: func(s *Session) <-chan interface{} {
				ch := make(chan interface{}, 1)
				AddHandlerTyped[GuildCreate](s, func(_ *Session, g *GuildCreate) { ch <- g })
				return ch
			},
			verify: func(t *testing.T, got interface{}) {
				t.Helper()
				g, ok := got.(*GuildCreate)
				if !ok {
					t.Fatalf("expected *GuildCreate, got %T", got)
				}
				if g.Guild == nil || g.Guild.ID != "g1" {
					t.Fatalf("unexpected event: %+v", g)
				}
			},
		},
		{
			name:      "Connect (synthetic event)",
			eventType: "__CONNECT__",
			payload:   &Connect{},
			register: func(s *Session) <-chan interface{} {
				ch := make(chan interface{}, 1)
				AddHandlerTyped(s, func(_ *Session, c *Connect) { ch <- c })
				return ch
			},
			verify: func(t *testing.T, got interface{}) {
				t.Helper()
				if _, ok := got.(*Connect); !ok {
					t.Fatalf("expected *Connect, got %T", got)
				}
			},
		},
		{
			name:      "RateLimit (synthetic event)",
			eventType: "__RATE_LIMIT__",
			payload:   &RateLimit{URL: "/channels/1/messages"},
			register: func(s *Session) <-chan interface{} {
				ch := make(chan interface{}, 1)
				AddHandlerTyped(s, func(_ *Session, r *RateLimit) { ch <- r })
				return ch
			},
			verify: func(t *testing.T, got interface{}) {
				t.Helper()
				r, ok := got.(*RateLimit)
				if !ok {
					t.Fatalf("expected *RateLimit, got %T", got)
				}
				if r.URL != "/channels/1/messages" {
					t.Fatalf("unexpected event: %+v", r)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New("Bot test-token")
			if err != nil {
				t.Fatal(err)
			}
			ch := tc.register(s)
			dispatchEvent(t, s, tc.eventType, tc.payload)
			// SyncEvents=true makes dispatch synchronous; a value must be
			// available without any wait.
			select {
			case got := <-ch:
				tc.verify(t, got)
			default:
				t.Fatal("typed handler was not called")
			}
		})
	}
}

// TestAddHandlerTypedZeroCopy checks that the typed handler receives the
// very same event instance the dispatch produced (no re-decode, no copy).
func TestAddHandlerTypedZeroCopy(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	ev := &MessageCreate{Message: &Message{ID: "m1"}}
	got := make(chan *MessageCreate, 1)
	AddHandlerTyped(s, func(_ *Session, m *MessageCreate) { got <- m })
	dispatchEvent(t, s, "MESSAGE_CREATE", ev)
	select {
	case m := <-got:
		if m != ev {
			t.Fatal("handler received a different event instance than dispatched")
		}
	default:
		t.Fatal("typed handler was not called")
	}
}

// TestAddHandlerTypedMultipleHandlers ensures typed handlers compose with
// the ordinary handler list (multiple handlers per event type, typed and
// untyped side by side).
func TestAddHandlerTypedMultipleHandlers(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	ev := &MessageCreate{Message: &Message{ID: "m1"}}
	var typedCalls, untypedCalls int
	AddHandlerTyped(s, func(*Session, *MessageCreate) { typedCalls++ })
	AddHandlerTyped(s, func(*Session, *MessageCreate) { typedCalls++ })
	s.AddHandler(func(_ *Session, m *MessageCreate) { untypedCalls++ })
	dispatchEvent(t, s, "MESSAGE_CREATE", ev)
	if typedCalls != 2 || untypedCalls != 1 {
		t.Fatalf("expected 2 typed and 1 untyped call, got %d and %d", typedCalls, untypedCalls)
	}
}

func TestAddHandlerTypedOnce(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	AddHandlerOnceTyped(s, func(*Session, *MessageCreate) { calls++ })
	dispatchEvent(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "mx"}})
	dispatchEvent(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "mx"}})
	if calls != 1 {
		t.Fatalf("expected once handler to fire exactly once, fired %d times", calls)
	}
}

func TestAddHandlerTypedRemove(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := AddHandlerTyped(s, func(*Session, *MessageCreate) { calls++ })
	remove()
	dispatchEvent(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "mx"}})
	if calls != 0 {
		t.Fatalf("expected removed handler to never fire, fired %d times", calls)
	}

	// Removing an already-removed handler must stay safe.
	remove()
	if calls != 0 {
		t.Fatalf("unexpected extra calls: %d", calls)
	}
}

func TestAddHandlerOnceTypedRemoveBeforeFire(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	remove := AddHandlerOnceTyped(s, func(*Session, *MessageCreate) { calls++ })
	remove()
	dispatchEvent(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "mx"}})
	if calls != 0 {
		t.Fatalf("expected removed once handler to never fire, fired %d times", calls)
	}
}

// TestAddHandlerTypedInvalidEventType covers the runtime guard: registering
// a typed handler for a type that is not a gateway event logs an error and
// returns an inert remover, mirroring AddHandler's behaviour for invalid
// handler functions.
func TestAddHandlerTypedInvalidEventType(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	remove := AddHandlerTyped(s, func(_ *Session, u *User) { called = true }) // User is not an event type
	if remove == nil {
		t.Fatal("expected non-nil (inert) remover")
	}
	remove() // must be safe
	dispatchEvent(t, s, "MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "mx"}})
	if called {
		t.Fatal("handler for non-event type must never be called")
	}
}

func TestWaitForEvent(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	// Dispatch a non-matching and a matching event from another goroutine
	// while the main goroutine waits.
	go func() {
		time.Sleep(10 * time.Millisecond)
		s.handleEvent("MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m0", Content: "no match"}})
		s.handleEvent("MESSAGE_CREATE", &MessageCreate{Message: &Message{ID: "m1", Content: "match me"}})
	}()

	start := time.Now()
	msg, ok := WaitForEvent(s, 2*time.Second, func(m *MessageCreate) bool {
		return m.Content == "match me"
	})
	if !ok {
		t.Fatal("expected to receive matching event")
	}
	if msg == nil || msg.ID != "m1" {
		t.Fatalf("unexpected event: %+v", msg)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("wait returned late: %s", elapsed)
	}

	// The internal handler must be removed after the wait.
	s.handlersMu.RLock()
	left := len(s.handlers["MESSAGE_CREATE"])
	s.handlersMu.RUnlock()
	if left != 0 {
		t.Fatalf("expected no leftover handlers, got %d", left)
	}
}

func TestWaitForEventTimeout(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	msg, ok := WaitForEvent[MessageCreate](s, 30*time.Millisecond, nil)
	if ok || msg != nil {
		t.Fatalf("expected timeout with nil result, got ok=%v msg=%v", ok, msg)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("returned before the deadline elapsed: %s", elapsed)
	}
}

func TestWaitForEventInvalidEventType(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := WaitForEvent[User](s, 10*time.Millisecond, nil) // User is not an event type
	if ok || msg != nil {
		t.Fatalf("expected immediate false for non-event type, got ok=%v msg=%v", ok, msg)
	}
}

func TestWaitForEventContextCancel(t *testing.T) {
	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	msg, ok := WaitForEventContext[MessageCreate](ctx, s, nil)
	if ok || msg != nil {
		t.Fatalf("expected cancelled context to return immediately, got ok=%v msg=%v", ok, msg)
	}
}
