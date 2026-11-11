package discordgo

import (
	"context"
	"time"
)

// This file adds compile-time type-safe event handling on top of the
// existing AddHandler / AddHandlerOnce machinery (event.go). The regular
// AddHandler accepts an interface{}, so a handler registered for the wrong
// event type (for example func(*Session, *Message) instead of
// func(*Session, *MessageUpdate)) is only reported at runtime — and the
// handler never fires. The typed functions below take a generic parameter
// for the event type, so the handler signature and the event are always
// consistent at compile time, while dispatch keeps the exact zero-copy
// behaviour of the generated handlers: no reflection, no per-event
// allocation, and full compatibility with Remove, once-handlers,
// SyncEvents, Session.DispatchWorkers and the raw-dispatch fast path.
//
// Example:
//
//	discordgo.AddHandlerTyped(s, func(s *discordgo.Session, m *discordgo.MessageCreate) {
//		if m.Author.Bot {
//			return
//		}
//		// ...
//	})
//
// or with the handler type spelled out explicitly:
//
//	discordgo.AddHandlerTyped[discordgo.GuildCreate](s, g.onGuildCreate)
//
// The event type parameter is the VALUE type (MessageCreate), not the
// pointer; handlers always receive a pointer to it.

// TypedHandlerFunc is a compile-time type-safe event handler for events of
// type E. It is the type-safe counterpart of the interface{} handlers taken
// by AddHandler.
type TypedHandlerFunc[E any] func(s *Session, e *E)

// typedEventHandler adapts a TypedHandlerFunc to the internal EventHandler
// interface so that typed handlers flow through the ordinary
// addEventHandler / once / remove machinery untouched.
type typedEventHandler[E any] struct {
	handler TypedHandlerFunc[E]
	etype   string
}

// Type returns the gateway event type this handler is registered for.
func (h typedEventHandler[E]) Type() string {
	return h.etype
}

// Handle performs the single type assertion from the event payload (the
// same assertion the generated per-event handlers in eventhandlers.go do)
// and calls the user handler.
func (h typedEventHandler[E]) Handle(s *Session, i interface{}) {
	if e, ok := i.(*E); ok {
		h.handler(s, e)
	}
}

// eventTypeFor resolves the gateway event type string (for example
// "MESSAGE_CREATE") for the event type E. It probes the existing
// handlerForInterface registry with an empty func(*Session, *E), so every
// event type known to AddHandler — Discord WSAPI events as well as
// synthetic events like Connect, Disconnect and RateLimit — is supported
// automatically, including events added in the future. It returns "" when E
// is not a known event type.
func eventTypeFor[E any]() string {
	eh := handlerForInterface(func(*Session, *E) {})
	if eh == nil {
		return ""
	}
	return eh.Type()
}

// AddHandlerTyped registers a type-safe event handler that will be fired
// any time the Discord WSAPI event of type E fires. It is the typed
// counterpart of AddHandler: the event type is part of the compile-time
// signature, so the handler can never be registered for the wrong event
// type. The return value is a function that, when called, removes the
// handler.
//
// If E is not an event type known to AddHandler (for example *User, or a
// pointer type such as *MessageCreate), an error is logged and the returned
// remover is a no-op — the same graceful behaviour AddHandler has for an
// invalid handler function.
func AddHandlerTyped[E any](s *Session, handler func(*Session, *E)) func() {
	return addTyped[E](s, handler, false)
}

// AddHandlerOnceTyped registers a type-safe event handler that will be
// fired the next time the Discord WSAPI event of type E fires, and then
// removed. It is the typed counterpart of AddHandlerOnce; see
// AddHandlerTyped for details.
func AddHandlerOnceTyped[E any](s *Session, handler func(*Session, *E)) func() {
	return addTyped[E](s, handler, true)
}

// addTyped is the shared implementation of AddHandlerTyped and
// AddHandlerOnceTyped.
func addTyped[E any](s *Session, handler func(*Session, *E), once bool) func() {
	etype := eventTypeFor[E]()
	if etype == "" {
		s.log(LogError, "Invalid typed handler: %T is not a known event type, handler will never be called", new(E))
		return func() {}
	}
	eh := typedEventHandler[E]{handler: handler, etype: etype}
	if once {
		return s.addEventHandlerOnce(eh)
	}
	return s.addEventHandler(eh)
}

// WaitForEvent blocks until the next event of type E arrives (optionally
// filtered), or until the timeout elapses. It reports the event and whether
// one was seen before the deadline. timeout <= 0 waits indefinitely. The
// underlying handler is always removed before returning, so repeated calls
// do not leak handlers.
//
// Example:
//
//	msg, ok := discordgo.WaitForEvent[discordgo.MessageCreate](s, 5*time.Second, nil)
//	if ok && msg.Content == "ping" {
//		// ...
//	}
func WaitForEvent[E any](s *Session, timeout time.Duration, filter func(*E) bool) (*E, bool) {
	if timeout <= 0 {
		return WaitForEventContext[E](context.Background(), s, filter)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return WaitForEventContext[E](ctx, s, filter)
}

// WaitForEventContext is WaitForEvent with a caller-supplied context; it
// returns as soon as a matching event arrives or the context is done.
func WaitForEventContext[E any](ctx context.Context, s *Session, filter func(*E) bool) (*E, bool) {
	etype := eventTypeFor[E]()
	if etype == "" {
		s.log(LogError, "Invalid typed wait: %T is not a known event type, wait will never complete", new(E))
		return nil, false
	}

	// Buffered so the dispatch goroutine never blocks if the waiter has
	// already timed out; the first matching event wins.
	ch := make(chan *E, 1)
	eh := typedEventHandler[E]{
		handler: func(_ *Session, e *E) {
			if filter != nil && !filter(e) {
				return
			}
			select {
			case ch <- e:
			default:
			}
		},
		etype: etype,
	}
	remove := s.addEventHandler(eh)
	defer remove()

	select {
	case e := <-ch:
		return e, true
	case <-ctx.Done():
		return nil, false
	}
}
