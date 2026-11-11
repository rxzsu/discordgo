package discordgo

// Composable middleware for typed event handlers (see typed.go).
//
// A TypedMiddleware wraps a TypedHandlerFunc and returns a new one. Because a
// middleware may simply not call next, the chain supports early exit: scope
// filters, deduplication and rate limiting all stop the chain before the
// wrapped handler runs.
//
//	discordgo.AddHandlerTypedWith(s, onMessage,
//		discordgo.RecoverTyped[discordgo.MessageCreate](),
//		discordgo.LogTyped[discordgo.MessageCreate]("onMessage"),
//		discordgo.FilterMessageCreateEvents(discordgo.ScopeFilter{GuildID: guildID}),
//		discordgo.DedupTyped[discordgo.MessageCreate](msgKey, time.Minute),
//		discordgo.ThrottleTyped[discordgo.MessageCreate](authorKey,
//			discordgo.ThrottleConfig[discordgo.MessageCreate]{Events: 5, Per: time.Second}),
//	)
//
// Middlewares apply left to right: the first middleware of the list is the
// outermost wrapper, so it runs first and observes everything the later
// middlewares and the handler do.

// TypedMiddleware wraps a TypedHandlerFunc[E] into another one. Returning a
// handler that never calls next exits the chain early.
type TypedMiddleware[E any] func(next TypedHandlerFunc[E]) TypedHandlerFunc[E]

// UseTyped wraps h with mws. The first middleware of mws is the outermost
// wrapper (it runs first). Nil middlewares are skipped, so UseTyped(h)
// returns h itself.
func UseTyped[E any](h TypedHandlerFunc[E], mws ...TypedMiddleware[E]) TypedHandlerFunc[E] {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] != nil {
			h = mws[i](h)
		}
	}
	return h
}

// ChainTyped combines mws into a single middleware with the same ordering
// rules as UseTyped. ChainTyped()(h) == h.
func ChainTyped[E any](mws ...TypedMiddleware[E]) TypedMiddleware[E] {
	return func(next TypedHandlerFunc[E]) TypedHandlerFunc[E] {
		return UseTyped(next, mws...)
	}
}

// AddHandlerTypedWith registers h wrapped with mws through AddHandlerTyped
// and returns the remover function.
func AddHandlerTypedWith[E any](s *Session, h TypedHandlerFunc[E], mws ...TypedMiddleware[E]) func() {
	return AddHandlerTyped[E](s, UseTyped(h, mws...))
}

// AddHandlerOnceTypedWith registers h wrapped with mws through
// AddHandlerOnceTyped and returns the remover function.
func AddHandlerOnceTypedWith[E any](s *Session, h TypedHandlerFunc[E], mws ...TypedMiddleware[E]) func() {
	return AddHandlerOnceTyped[E](s, UseTyped(h, mws...))
}

// TypedPipeline accumulates middlewares once and registers any number of
// handlers through them. It is safe to share one pipeline across sessions,
// but Use is not synchronised: configure the pipeline before registering.
type TypedPipeline[E any] struct {
	mws []TypedMiddleware[E]
}

// NewTypedPipeline returns a pipeline preloaded with mws.
func NewTypedPipeline[E any](mws ...TypedMiddleware[E]) *TypedPipeline[E] {
	p := &TypedPipeline[E]{}
	p.Use(mws...)
	return p
}

// Use appends middlewares to the pipeline and returns p for chaining. Nil
// middlewares are skipped.
func (p *TypedPipeline[E]) Use(mws ...TypedMiddleware[E]) *TypedPipeline[E] {
	for _, mw := range mws {
		if mw != nil {
			p.mws = append(p.mws, mw)
		}
	}
	return p
}

// Middleware returns the accumulated middlewares as a single middleware.
func (p *TypedPipeline[E]) Middleware() TypedMiddleware[E] {
	return ChainTyped(p.mws...)
}

// Handler wraps h with the pipeline middlewares.
func (p *TypedPipeline[E]) Handler(h TypedHandlerFunc[E]) TypedHandlerFunc[E] {
	return UseTyped(h, p.mws...)
}

// Register registers h through AddHandlerTypedWith and returns the remover.
func (p *TypedPipeline[E]) Register(s *Session, h TypedHandlerFunc[E]) func() {
	return AddHandlerTypedWith[E](s, h, p.mws...)
}

// RegisterOnce registers h through AddHandlerOnceTypedWith and returns the
// remover.
func (p *TypedPipeline[E]) RegisterOnce(s *Session, h TypedHandlerFunc[E]) func() {
	return AddHandlerOnceTypedWith[E](s, h, p.mws...)
}
