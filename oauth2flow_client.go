// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements OAuth2 token lifecycle management on top of the
// authorization-code flow in oauth2flow_token.go:
//
//   - TokenManager: holds the current token, refreshes it lazily under a
//     mutex (single refresh for any number of concurrent callers), can run
//     a background auto-refresh goroutine, and persists the token as JSON.
//   - OAuth2Transport: an http.RoundTripper that injects the bearer token
//     into requests and restarts a request once after a 401 by forcing a
//     token refresh.
//
// Everything here builds only on the public surface of the package.

package discordgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"sync"
	"time"
)

// OAuth2TokenSource produces a currently valid access token on demand and
// can be forced to fetch a new one. TokenManager is the primary
// implementation; the interface exists so callers can plug in their own
// sources (tests, static tokens, upstream identity providers).
type OAuth2TokenSource interface {
	// Token returns the current access token, refreshing it first if it
	// is expired or about to expire.
	Token(ctx context.Context) (string, error)
	// ForceRefresh discards the current access token and obtains a new
	// one, even if the old one has not expired yet.
	ForceRefresh(ctx context.Context) error
}

// TokenManagerOptions tune a TokenManager. Zero values mean the documented
// defaults.
type TokenManagerOptions struct {
	// RefreshEarly is how long before expiry a token counts as stale and
	// gets refreshed by Token and the auto-refresher. Default 30s.
	RefreshEarly time.Duration
	// RefreshJitter is the maximum random amount added to RefreshEarly,
	// so that many managers refreshed around the same time do not all hit
	// the token endpoint in the same second. Default 10s.
	RefreshJitter time.Duration
	// OnTokenChange is invoked with the new token whenever the manager
	// stores a token (exchange, refresh, SetToken, LoadJSON). It is called
	// while the manager's mutex is held: the callback must not call back
	// into the manager or it will deadlock. Use it for persistence.
	OnTokenChange func(*OAuth2Token)
}

// resolved defaults for TokenManagerOptions.
const (
	defaultOAuth2RefreshEarly  = 30 * time.Second
	defaultOAuth2RefreshJitter = 10 * time.Second
	// oAuth2AutoPollInterval is how often the auto-refresher re-evaluates
	// when there is no token (yet) or the token has no expiry.
	oAuth2AutoPollInterval = time.Second
)

// TokenManager manages the lifecycle of a single OAuth2 token: lazy
// refresh-on-demand, optional background auto-refresh and JSON
// persistence. It implements OAuth2TokenSource and is safe for concurrent
// use: concurrent Token calls while a refresh is in flight all block on
// one refresh.
type TokenManager struct {
	flow *OAuth2Flow

	mu            sync.Mutex
	tok           *OAuth2Token
	opts          TokenManagerOptions
	refreshEarly  time.Duration
	refreshJitter time.Duration
	onTokenChange func(*OAuth2Token)
	closed        bool

	stop     chan struct{}
	stopOnce sync.Once
	autoWG   sync.WaitGroup
}

// compile-time assertion that TokenManager provides OAuth2TokenSource.
var _ OAuth2TokenSource = (*TokenManager)(nil)

// NewTokenManager creates a TokenManager for the given client
// configuration.
func NewTokenManager(cfg OAuth2ClientConfig, opts TokenManagerOptions) *TokenManager {
	tm := &TokenManager{
		flow: NewOAuth2Flow(cfg),
		opts: opts,
	}
	tm.refreshEarly = opts.RefreshEarly
	if tm.refreshEarly <= 0 {
		tm.refreshEarly = defaultOAuth2RefreshEarly
	}
	tm.refreshJitter = opts.RefreshJitter
	if tm.refreshJitter < 0 {
		tm.refreshJitter = defaultOAuth2RefreshJitter
	}
	tm.onTokenChange = opts.OnTokenChange
	return tm
}

// Flow returns the underlying OAuth2Flow, which exposes the authorize URL
// builder and raw exchange/refresh operations.
func (tm *TokenManager) Flow() *OAuth2Flow {
	return tm.flow
}

// SetToken stores an externally obtained token (e.g. loaded from a
// datastore at startup).
func (tm *TokenManager) SetToken(t *OAuth2Token) {
	if t == nil {
		return
	}
	finalizeOAuth2Token(t)
	tm.mu.Lock()
	tm.setTokenLocked(t)
	tm.mu.Unlock()
}

// Current returns a copy of the currently stored token without refreshing
// it. ok is false when no token has been set or exchanged yet.
func (tm *TokenManager) Current() (t OAuth2Token, ok bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.tok == nil {
		return OAuth2Token{}, false
	}
	return *tm.tok.clone(), true
}

// ExchangeCode exchanges an authorization code for a token and stores it
// as the current token.
func (tm *TokenManager) ExchangeCode(ctx context.Context, code string) error {
	tok, err := tm.flow.ExchangeCode(ctx, code)
	if err != nil {
		return err
	}
	tm.SetToken(tok)
	return nil
}

// Token implements OAuth2TokenSource. It returns the stored access token,
// refreshing it first when it is expired or expires within the refresh
// lead time. Concurrent callers share a single refresh.
func (tm *TokenManager) Token(ctx context.Context) (string, error) {
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return "", ErrOAuth2TokenClosed
	}
	if tm.tok == nil {
		tm.mu.Unlock()
		return "", ErrOAuth2NoToken
	}

	// Fast path: token usable and not due for a refresh.
	if tm.tok.AccessToken != "" && !tm.expiresSoonLocked() {
		token := tm.tok.AccessToken
		tm.mu.Unlock()
		return token, nil
	}

	// The token expired (or expires soon). Without a refresh token there
	// is nothing to do: serve a not-yet-expired token as a last resort.
	if tm.tok.RefreshToken == "" {
		if !tm.tok.Expired() && tm.tok.AccessToken != "" {
			token := tm.tok.AccessToken
			tm.mu.Unlock()
			return token, nil
		}
		tm.mu.Unlock()
		return "", ErrOAuth2NoRefreshToken
	}

	tok, err := tm.flow.RefreshToken(ctx, tm.tok.RefreshToken)
	if err != nil {
		tm.mu.Unlock()
		return "", err
	}
	tm.setTokenLocked(tok)
	token := tok.AccessToken
	tm.mu.Unlock()
	return token, nil
}

// ForceRefresh implements OAuth2TokenSource. It obtains a new token via
// the refresh grant even if the current one has not expired.
func (tm *TokenManager) ForceRefresh(ctx context.Context) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.closed {
		return ErrOAuth2TokenClosed
	}
	if tm.tok == nil {
		return ErrOAuth2NoToken
	}
	if tm.tok.RefreshToken == "" {
		return ErrOAuth2NoRefreshToken
	}
	tok, err := tm.flow.RefreshToken(ctx, tm.tok.RefreshToken)
	if err != nil {
		return err
	}
	tm.setTokenLocked(tok)
	return nil
}

// setTokenLocked stores tok and fires the change callback. Callers must
// hold tm.mu.
func (tm *TokenManager) setTokenLocked(tok *OAuth2Token) {
	tm.tok = tok
	if fn := tm.onTokenChange; fn != nil && tok != nil {
		fn(tok.clone())
	}
}

// refreshLeadLocked returns the lead time before expiry at which the
// token counts as stale, jittered per call. Callers must hold tm.mu.
func (tm *TokenManager) refreshLeadLocked() time.Duration {
	if tm.refreshJitter <= 0 {
		return tm.refreshEarly
	}
	return tm.refreshEarly + time.Duration(rand.Int64N(int64(tm.refreshJitter)))
}

// expiresSoonLocked reports whether the current token expires within the
// jittered refresh lead. Callers must hold tm.mu.
func (tm *TokenManager) expiresSoonLocked() bool {
	return tm.tok.ExpiresWithin(tm.refreshLeadLocked())
}

// StartAutoRefresh starts a background goroutine that refreshes the token
// shortly before it expires. It is safe to start before any token has been
// stored: the goroutine polls until a token appears. Calling it twice is a
// no-op. The goroutine stops when ctx is cancelled or Close is called.
func (tm *TokenManager) StartAutoRefresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	tm.mu.Lock()
	if tm.closed {
		tm.mu.Unlock()
		return ErrOAuth2TokenClosed
	}
	if tm.stop != nil {
		tm.mu.Unlock()
		return nil // already running
	}
	tm.stop = make(chan struct{})
	stop := tm.stop
	tm.autoWG.Add(1)
	tm.mu.Unlock()

	go func() {
		defer tm.autoWG.Done()
		for {
			delay := tm.nextAutoDelay()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
			}
			// Best effort: a failed refresh is retried on the next tick
			// and the lazy Token path still refreshes on demand.
			_ = tm.refreshIfDue(ctx)
		}
	}()
	return nil
}

// nextAutoDelay computes how long the auto-refresher should sleep before
// its next action.
func (tm *TokenManager) nextAutoDelay() time.Duration {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.closed {
		return time.Hour // Close will close stop and wake us; delay is moot
	}
	if tm.tok == nil || tm.tok.RefreshToken == "" || tm.tok.Expiry.IsZero() {
		return oAuth2AutoPollInterval
	}
	lead := tm.refreshLeadLocked()
	due := tm.tok.Expiry.Add(-lead)
	delay := time.Until(due)
	if delay < 0 {
		// Already due (or overdue): act on the next tick.
		return time.Duration(0)
	}
	return delay
}

// refreshIfDue refreshes the token only when it is expired or expiring
// within the refresh lead.
func (tm *TokenManager) refreshIfDue(ctx context.Context) error {
	tm.mu.Lock()
	if tm.closed || tm.tok == nil || tm.tok.RefreshToken == "" || !tm.expiresSoonLocked() {
		tm.mu.Unlock()
		return nil
	}
	refresh := tm.tok.RefreshToken
	tm.mu.Unlock()

	tok, err := tm.flow.RefreshToken(ctx, refresh)
	if err != nil {
		// TokenManager may have been closed while refreshing; report that
		// instead of storing a token in a closed manager.
		tm.mu.Lock()
		closed := tm.closed
		tm.mu.Unlock()
		if closed {
			return ErrOAuth2TokenClosed
		}
		return err
	}

	// Store the refreshed token only if it still belongs to the same
	// refresh chain: another caller may have advanced it meanwhile.
	tm.mu.Lock()
	if !tm.closed && tm.tok != nil && tm.tok.RefreshToken == refresh {
		tm.setTokenLocked(tok)
	}
	tm.mu.Unlock()
	return nil
}

// Close stops the auto-refresh goroutine and makes the manager refuse new
// refreshes. Token requests already in flight may still complete.
func (tm *TokenManager) Close() {
	tm.stopOnce.Do(func() {
		tm.mu.Lock()
		if tm.closed {
			tm.mu.Unlock()
			return
		}
		tm.closed = true
		stop := tm.stop
		tm.mu.Unlock()
		if stop != nil {
			close(stop)
		}
		tm.autoWG.Wait()
	})
}

// ------------------------------------------------------------------------------------------------
// JSON persistence
// ------------------------------------------------------------------------------------------------

// SaveJSON serializes the current token. The result can be stored and fed
// back via LoadJSON after a restart.
func (tm *TokenManager) SaveJSON() ([]byte, error) {
	tm.mu.Lock()
	tok := tm.tok.clone()
	tm.mu.Unlock()
	if tok == nil {
		return nil, ErrOAuth2NoToken
	}
	return json.Marshal(tok)
}

// LoadJSON replaces the current token with the serialized token in data.
func (tm *TokenManager) LoadJSON(data []byte) error {
	var tok OAuth2Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return fmt.Errorf("oauth2: decoding stored token: %w", err)
	}
	if tok.AccessToken == "" {
		return ErrOAuth2InvalidTokenResponse
	}
	tm.SetToken(&tok)
	return nil
}

// SaveFile writes SaveJSON output to path.
func (tm *TokenManager) SaveFile(path string) error {
	data, err := tm.SaveJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadFile reads a token previously written by SaveFile.
func (tm *TokenManager) LoadFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return tm.LoadJSON(data)
}

// ------------------------------------------------------------------------------------------------
// HTTP client binding
// ------------------------------------------------------------------------------------------------

// HTTPClient returns an *http.Client that authenticates its requests with
// the managed token: every request carries Authorization: <scheme> <token>
// and is restarted once after a 401 with a freshly forced refresh.
//
// The optional base client supplies timeout, redirect policy and
// transport; a nil base means http.DefaultClient semantics.
func (tm *TokenManager) HTTPClient(base *http.Client) *http.Client {
	c := &http.Client{}
	if base != nil {
		*c = *base
	}
	var baseTransport http.RoundTripper
	if base != nil && base.Transport != nil {
		baseTransport = base.Transport
	}
	c.Transport = &OAuth2Transport{Source: tm, Base: baseTransport}
	return c
}

// AttachSession binds the manager to a Discordgo session so that all REST
// calls made through the session are authenticated with the managed user
// token instead of the session's bot token. The session's token is cleared
// (its request builder would otherwise stamp a stale Authorization header)
// and its HTTP client is wrapped with OAuth2Transport.
func (tm *TokenManager) AttachSession(s *Session) {
	if s == nil {
		return
	}
	tm.mu.Lock()
	closed := tm.closed
	tm.mu.Unlock()
	if closed {
		return
	}
	s.Client = tm.HTTPClient(s.Client)
	s.Token = ""
}

// ------------------------------------------------------------------------------------------------
// RoundTripper
// ------------------------------------------------------------------------------------------------

// OAuth2Transport is an http.RoundTripper that injects the token from an
// OAuth2TokenSource into outgoing requests and restarts a request once
// after a 401 response, forcing a token refresh in between. Use it via
// TokenManager.HTTPClient or wrap any client's transport manually.
type OAuth2Transport struct {
	// Base is the wrapped transport; nil means http.DefaultTransport.
	Base http.RoundTripper
	// Source provides (and refreshes) the access token.
	Source OAuth2TokenSource
	// Scheme is the authorization scheme used in the header. Empty means
	// "Bearer".
	Scheme string
}

// NewOAuth2Transport wraps base with OAuth2 token injection and 401
// restart for the given source.
func NewOAuth2Transport(src OAuth2TokenSource, base http.RoundTripper) *OAuth2Transport {
	return &OAuth2Transport{Source: src, Base: base}
}

// ErrOAuth2RefreshAfter401 wraps a refresh failure that happened while
// handling a 401. The original response body has been closed at that
// point, so callers get this error instead of the response.
var ErrOAuth2RefreshAfter401 = errors.New("oauth2: request rejected with 401 and token refresh failed")

// RoundTrip implements http.RoundTripper.
func (t *OAuth2Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	scheme := t.Scheme
	if scheme == "" {
		scheme = "Bearer"
	}

	tok, err := t.Source.Token(req.Context())
	if err != nil {
		return nil, err
	}
	resp, err := base.RoundTrip(t.authorize(req, scheme, tok))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	// The token was rejected server-side (expired ahead of schedule,
	// revoked, or rotated). Force a refresh and restart the request once.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if err := t.Source.ForceRefresh(req.Context()); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuth2RefreshAfter401, err)
	}
	fresh, err := t.Source.Token(req.Context())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOAuth2RefreshAfter401, err)
	}
	return base.RoundTrip(t.authorize(req, scheme, fresh))
}

// authorize returns a clone of req carrying the Authorization header.
// Bodies are rewound through GetBody so a restarted request (after a 401)
// sends the payload again instead of an exhausted reader.
func (t *OAuth2Transport) authorize(req *http.Request, scheme, token string) *http.Request {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", scheme+" "+token)
	if clone.Body != nil && clone.GetBody != nil {
		if body, err := clone.GetBody(); err == nil {
			clone.Body = body
		}
	}
	return clone
}
