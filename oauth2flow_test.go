package discordgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ------------------------------------------------------------------------------------------------
// Fake OAuth2 token endpoint
// ------------------------------------------------------------------------------------------------

// fakeTokenEndpoint is a httptest-backed implementation of the Discord
// OAuth2 token endpoint, recording grant requests like the fork tests do.
type fakeTokenEndpoint struct {
	srv *httptest.Server

	mu          sync.Mutex
	exchanges   int // authorization_code grants
	refreshes   int // refresh_token grants
	lastForm    url.Values
	failRefresh bool // answer 400 invalid_grant to refresh requests
}

// newFakeTokenEndpoint starts a fake token endpoint and returns a client
// config pointed at it.
func newFakeTokenEndpoint(t *testing.T) (*fakeTokenEndpoint, OAuth2ClientConfig, func()) {
	t.Helper()

	ft := &fakeTokenEndpoint{}
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		ft.mu.Lock()
		defer ft.mu.Unlock()
		ft.lastForm = r.PostForm
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			ft.exchanges++
		case "refresh_token":
			ft.refreshes++
			if ft.failRefresh {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "invalid_grant", "error_description": "token expired"}`))
				return
			}
		default:
			http.Error(w, "unsupported grant", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"access_token":  fmt.Sprintf("access-%d", ft.exchanges+ft.refreshes),
			"token_type":    "Bearer",
			"refresh_token": "refresh-" + r.PostForm.Get("refresh_token"),
			"expires_in":    3600,
			"scope":         "identify email",
		}
		if rt := r.PostForm.Get("refresh_token"); rt == "" && ft.exchanges > 0 {
			resp["refresh_token"] = fmt.Sprintf("refresh-%d", ft.exchanges)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	ft.srv = httptest.NewServer(mux)
	cfg := OAuth2ClientConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		RedirectURI:  "https://app.example.com/callback",
		TokenURL:     ft.srv.URL + "/oauth2/token",
		AuthURL:      "https://discord.com/oauth2/authorize",
	}
	return ft, cfg, ft.srv.Close
}

// grantCounts returns the number of code exchanges and refresh grants so
// far.
func (ft *fakeTokenEndpoint) grantCounts() (exchanges, refreshes int) {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.exchanges, ft.refreshes
}

// form returns the last received form values.
func (ft *fakeTokenEndpoint) form() url.Values {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return ft.lastForm
}

// setFailRefresh makes future refresh_token grants fail.
func (ft *fakeTokenEndpoint) setFailRefresh(v bool) {
	ft.mu.Lock()
	ft.failRefresh = v
	ft.mu.Unlock()
}

// ------------------------------------------------------------------------------------------------
// Flow: authorize URL, exchange, refresh
// ------------------------------------------------------------------------------------------------

func TestOAuth2AuthCodeURL(t *testing.T) {
	f := NewOAuth2Flow(OAuth2ClientConfig{
		ClientID:    "42",
		RedirectURI: "https://app.example.com/cb",
		AuthURL:     "https://discord.com/oauth2/authorize",
	})
	got := f.AuthCodeURL("state123", OAuth2ScopeIdentify, OAuth2ScopeEmail)

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("bad URL %q: %v", got, err)
	}
	if u.Scheme != "https" || u.Host != "discord.com" || u.Path != "/oauth2/authorize" {
		t.Fatalf("unexpected authorize URL base: %s", got)
	}
	q := u.Query()
	for _, tc := range []struct{ key, want string }{
		{"client_id", "42"},
		{"redirect_uri", "https://app.example.com/cb"},
		{"response_type", "code"},
		{"scope", "identify email"},
		{"state", "state123"},
	} {
		if got := q.Get(tc.key); got != tc.want {
			t.Fatalf("query %s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestOAuth2AuthCodeURLWithParams(t *testing.T) {
	f := NewOAuth2Flow(OAuth2ClientConfig{ClientID: "42", RedirectURI: "cb"})
	got := f.AuthCodeURLWithParams("", OAuth2AuthCodeParams{
		GuildID:            "g1",
		Permissions:        8,
		DisableGuildSelect: true,
		Prompt:             "consent",
	}, OAuth2ScopeBot)

	q, err := url.ParseQuery(strings.TrimPrefix(got, "https://discord.com/oauth2/authorize?"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("guild_id") != "g1" || q.Get("permissions") != "8" ||
		q.Get("disable_guild_select") != "true" || q.Get("prompt") != "consent" ||
		q.Get("scope") != "bot" {
		t.Fatalf("unexpected params: %v", q)
	}
}

func TestOAuth2ExchangeCode(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	f := NewOAuth2Flow(cfg)

	tok, err := f.ExchangeCode(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if !strings.HasPrefix(tok.AccessToken, "access-") {
		t.Fatalf("unexpected access token %q", tok.AccessToken)
	}
	if tok.TokenType != "Bearer" {
		t.Fatalf("token type = %q, want Bearer", tok.TokenType)
	}
	if tok.RefreshToken == "" {
		t.Fatal("expected refresh token")
	}
	if tok.Expiry.IsZero() {
		t.Fatal("expected computed Expiry")
	}
	if until := time.Until(tok.Expiry); until <= 0 || until > 2*time.Hour {
		t.Fatalf("expiry %v out of range: %v", tok.Expiry, until)
	}
	if !tok.HasScope(OAuth2ScopeIdentify) || !tok.HasScope(OAuth2ScopeEmail) ||
		tok.HasScope(OAuth2ScopeGuilds) {
		t.Fatalf("unexpected scope parsing: %q", tok.Scope)
	}

	form := ft.form()
	for key, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "the-code",
		"redirect_uri":  cfg.RedirectURI,
		"client_id":     cfg.ClientID,
		"client_secret": cfg.ClientSecret,
	} {
		if got := form.Get(key); got != want {
			t.Fatalf("form %s = %q, want %q", key, got, want)
		}
	}
}

func TestOAuth2ExchangeCodeEmpty(t *testing.T) {
	f := NewOAuth2Flow(OAuth2ClientConfig{})
	if _, err := f.ExchangeCode(context.Background(), ""); !errors.Is(err, ErrOAuth2EmptyCode) {
		t.Fatalf("expected ErrOAuth2EmptyCode, got %v", err)
	}
}

func TestOAuth2RefreshToken(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	f := NewOAuth2Flow(cfg)

	tok, err := f.RefreshToken(context.Background(), "rt-1")
	if err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken != "refresh-rt-1" {
		t.Fatalf("unexpected token: %+v", tok)
	}
	form := ft.form()
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "rt-1" {
		t.Fatalf("unexpected form: %v", form)
	}
	if _, err := f.RefreshToken(context.Background(), ""); !errors.Is(err, ErrOAuth2EmptyRefreshToken) {
		t.Fatalf("expected ErrOAuth2EmptyRefreshToken, got %v", err)
	}
}

func TestOAuth2TokenEndpointError(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	ft.setFailRefresh(true)
	f := NewOAuth2Flow(cfg)

	_, err := f.RefreshToken(context.Background(), "rt-bad")
	var oauthErr *OAuth2Error
	if !errors.As(err, &oauthErr) {
		t.Fatalf("expected *OAuth2Error, got %T: %v", err, err)
	}
	if oauthErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", oauthErr.StatusCode)
	}
	if oauthErr.ErrorCode != "invalid_grant" {
		t.Fatalf("error code = %q, want invalid_grant", oauthErr.ErrorCode)
	}
	if !strings.Contains(oauthErr.Error(), "invalid_grant") {
		t.Fatalf("Error() should mention the oauth2 code: %v", oauthErr)
	}
}

func TestOAuth2TokenHelpers(t *testing.T) {
	var tok OAuth2Token
	if tok.Valid() {
		t.Fatal("empty token must not be valid")
	}
	tok.AccessToken = "abc"
	if !tok.Valid() {
		t.Fatal("token without expiry must be valid")
	}
	if got := tok.AuthHeader(); got != "Bearer abc" {
		t.Fatalf("AuthHeader = %q, want Bearer abc", got)
	}
	tok.TokenType = "Basic"
	if got := tok.AuthHeader(); got != "Basic abc" {
		t.Fatalf("AuthHeader = %q, want Basic abc", got)
	}

	past := OAuth2Token{AccessToken: "x", Expiry: time.Now().Add(-time.Minute)}
	if !past.Expired() || past.Valid() {
		t.Fatal("past token must be expired and invalid")
	}
	if !past.ExpiresWithin(time.Hour) || past.ExpiresWithin(-time.Hour) {
		t.Fatal("ExpiresWithin misbehaves for past token")
	}
	future := OAuth2Token{AccessToken: "x", Expiry: time.Now().Add(time.Minute)}
	if future.Expired() || future.ExpiresWithin(-time.Minute) {
		t.Fatal("future token must not be expired")
	}
}

func TestOAuth2ScopeHelpers(t *testing.T) {
	if got := JoinOAuth2Scopes(OAuth2ScopeIdentify, "", OAuth2ScopeEmail); got != "identify email" {
		t.Fatalf("JoinOAuth2Scopes = %q", got)
	}
	scopes := ParseOAuth2Scopes("identify  email identify guilds.join")
	if len(scopes) != 3 {
		t.Fatalf("ParseOAuth2Scopes len = %d, want 3: %v", len(scopes), scopes)
	}
	if !HasOAuth2Scope(scopes, OAuth2ScopeGuildsJoin) || HasOAuth2Scope(scopes, OAuth2ScopeBot) {
		t.Fatal("HasOAuth2Scope misbehaves")
	}
	if got := OAuth2ScopesIdentificationEmail(); len(got) != 2 {
		t.Fatalf("OAuth2ScopesIdentificationEmail = %v", got)
	}
	if got := OAuth2ScopesGuildsJoin(); !HasOAuth2Scope(got, OAuth2ScopeGuildsJoin) {
		t.Fatalf("OAuth2ScopesGuildsJoin = %v", got)
	}
}

// ------------------------------------------------------------------------------------------------
// TokenManager
// ------------------------------------------------------------------------------------------------

func TestTokenManagerLazyRefresh(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()

	tm := NewTokenManager(cfg, TokenManagerOptions{RefreshEarly: 50 * time.Millisecond})
	tok := &OAuth2Token{AccessToken: "initial", RefreshToken: "rt", Expiry: time.Now().Add(200 * time.Millisecond)}
	tm.SetToken(tok)

	// Fresh: no refresh.
	got, err := tm.Token(context.Background())
	if err != nil || got != "initial" {
		t.Fatalf("Token() = %q, %v; want initial", got, err)
	}
	if _, r := ft.grantCounts(); r != 0 {
		t.Fatalf("unexpected refresh, got %d", r)
	}

	// Wait until inside the refresh lead, then expect exactly one refresh.
	deadline := tok.Expiry.Add(-50 * time.Millisecond)
	time.Sleep(time.Until(deadline) + 20*time.Millisecond)
	got, err = tm.Token(context.Background())
	if err != nil || !strings.HasPrefix(got, "access-") {
		t.Fatalf("Token() = %q, %v; want refreshed access token", got, err)
	}
	if _, r := ft.grantCounts(); r != 1 {
		t.Fatalf("refreshes = %d, want 1", r)
	}
}

func TestTokenManagerConcurrentRefresh(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()

	tm := NewTokenManager(cfg, TokenManagerOptions{RefreshEarly: 0, RefreshJitter: 0})
	// refreshEarly 0 falls back to the default; force expiry via a token
	// already expired relative to any lead.
	tm.SetToken(&OAuth2Token{AccessToken: "stale", RefreshToken: "rt", Expiry: time.Now().Add(-time.Hour)})

	var wg sync.WaitGroup
	var firstErr error
	errs := make([]error, 20)
	results := make([]string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := tm.Token(context.Background())
			results[i] = tok
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			firstErr = errs[i]
			break
		}
	}
	if firstErr != nil {
		t.Fatalf("Token() error: %v", firstErr)
	}
	unique := map[string]bool{}
	for _, r := range results {
		unique[r] = true
	}
	if len(unique) != 1 {
		t.Fatalf("concurrent Token() returned %d distinct tokens", len(unique))
	}
	if _, r := ft.grantCounts(); r != 1 {
		t.Fatalf("refreshes = %d, want exactly 1 (singleflight via mutex)", r)
	}
}

func TestTokenManagerForceRefreshAndErrors(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()

	tm := NewTokenManager(cfg, TokenManagerOptions{})
	if _, err := tm.Token(context.Background()); !errors.Is(err, ErrOAuth2NoToken) {
		t.Fatalf("expected ErrOAuth2NoToken, got %v", err)
	}
	if err := tm.ForceRefresh(context.Background()); !errors.Is(err, ErrOAuth2NoToken) {
		t.Fatalf("expected ErrOAuth2NoToken from ForceRefresh, got %v", err)
	}

	tm.SetToken(&OAuth2Token{AccessToken: "a1", Expiry: time.Now().Add(time.Hour)})
	if err := tm.ForceRefresh(context.Background()); !errors.Is(err, ErrOAuth2NoRefreshToken) {
		t.Fatalf("expected ErrOAuth2NoRefreshToken, got %v", err)
	}

	tm.SetToken(&OAuth2Token{AccessToken: "a1", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)})
	if err := tm.ForceRefresh(context.Background()); err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if _, r := ft.grantCounts(); r != 1 {
		t.Fatalf("refreshes = %d, want 1", r)
	}
	cur, ok := tm.Current()
	if !ok || !strings.HasPrefix(cur.AccessToken, "access-") {
		t.Fatalf("Current() = %+v, ok=%v", cur, ok)
	}
}

func TestTokenManagerExchangeAndOnTokenChange(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()

	var changes atomic.Int32
	var lastSeen string
	tm := NewTokenManager(cfg, TokenManagerOptions{OnTokenChange: func(tok *OAuth2Token) {
		changes.Add(1)
		lastSeen = tok.AccessToken
	}})
	if err := tm.ExchangeCode(context.Background(), "code-1"); err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if changes.Load() != 1 || lastSeen == "" {
		t.Fatalf("OnTokenChange not fired: %d", changes.Load())
	}
	if e, _ := ft.grantCounts(); e != 1 {
		t.Fatalf("exchanges = %d, want 1", e)
	}
}

func TestTokenManagerPersistence(t *testing.T) {
	_, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()

	src := NewTokenManager(cfg, TokenManagerOptions{})
	expiry := time.Now().Add(30 * time.Minute).Round(time.Second)
	src.SetToken(&OAuth2Token{AccessToken: "persist-me", RefreshToken: "rt", Scope: "identify", Expiry: expiry})

	data, err := src.SaveJSON()
	if err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	dst := NewTokenManager(cfg, TokenManagerOptions{})
	if err := dst.LoadJSON(data); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	cur, ok := dst.Current()
	if !ok || cur.AccessToken != "persist-me" || cur.RefreshToken != "rt" || !cur.Expiry.Equal(expiry) {
		t.Fatalf("roundtrip mismatch: %+v (want expiry %v)", cur, expiry)
	}

	// File roundtrip.
	path := t.TempDir() + "/token.json"
	if err := dst.SaveFile(path); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	dst2 := NewTokenManager(cfg, TokenManagerOptions{})
	if err := dst2.LoadFile(path); err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if cur, ok := dst2.Current(); !ok || cur.AccessToken != "persist-me" {
		t.Fatalf("file roundtrip mismatch: %+v", cur)
	}

	// Garbage input.
	if err := dst2.LoadJSON([]byte("not json")); err == nil {
		t.Fatal("expected error decoding garbage")
	}
	if err := dst2.LoadJSON([]byte(`{"access_token": ""}`)); !errors.Is(err, ErrOAuth2InvalidTokenResponse) {
		t.Fatalf("expected ErrOAuth2InvalidTokenResponse, got %v", err)
	}
}

func TestTokenManagerAutoRefresh(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()

	tm := NewTokenManager(cfg, TokenManagerOptions{RefreshEarly: 30 * time.Millisecond})
	tm.SetToken(&OAuth2Token{AccessToken: "first", RefreshToken: "rt", Expiry: time.Now().Add(150 * time.Millisecond)})

	if err := tm.StartAutoRefresh(context.Background()); err != nil {
		t.Fatalf("StartAutoRefresh: %v", err)
	}
	// Second start is a no-op.
	if err := tm.StartAutoRefresh(context.Background()); err != nil {
		t.Fatalf("second StartAutoRefresh: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, r := ft.grantCounts(); r > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, r := ft.grantCounts(); r == 0 {
		t.Fatal("auto-refresher did not refresh before expiry")
	}
	cur, _ := tm.Current()
	if cur.AccessToken == "first" {
		t.Fatal("token not replaced by auto-refresher")
	}

	tm.Close()
	tm.Close() // idempotent
	// After Close the manager refuses further refreshes.
	if err := tm.ForceRefresh(context.Background()); !errors.Is(err, ErrOAuth2TokenClosed) {
		t.Fatalf("expected ErrOAuth2TokenClosed after Close, got %v", err)
	}
	if err := tm.StartAutoRefresh(context.Background()); !errors.Is(err, ErrOAuth2TokenClosed) {
		t.Fatalf("expected ErrOAuth2TokenClosed from StartAutoRefresh, got %v", err)
	}
}

// ------------------------------------------------------------------------------------------------
// OAuth2Transport
// ------------------------------------------------------------------------------------------------

// newFakeDiscordAPI starts a server that rejects requests carrying the
// stale bearer token with 401 and accepts anything else, recording the
// Authorization headers it saw.
func newFakeDiscordAPI(t *testing.T, staleToken string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		seen = append(seen, auth)
		mu.Unlock()
		if strings.HasSuffix(auth, staleToken) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "invalid_token"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestOAuth2TransportRestartsOn401(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	tm := NewTokenManager(cfg, TokenManagerOptions{})
	tm.SetToken(&OAuth2Token{AccessToken: "stale-token", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)})

	api, seen := newFakeDiscordAPI(t, "stale-token")
	defer api.Close()

	client := tm.HTTPClient(&http.Client{Timeout: 5 * time.Second})

	// POST with a body exercises request cloning through the restart.
	req, err := http.NewRequest(http.MethodPost, api.URL+"/users/@me", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") {
		t.Fatalf("unexpected response: %d %s", resp.StatusCode, body)
	}

	headers := seen()
	if len(headers) != 2 {
		t.Fatalf("expected 2 API requests (401 + restart), got %d: %v", len(headers), headers)
	}
	if !strings.HasSuffix(headers[0], "stale-token") {
		t.Fatalf("first request should carry the stale token: %v", headers[0])
	}
	if _, r := ft.grantCounts(); r != 1 {
		t.Fatalf("refreshes = %d, want 1", r)
	}
	cur, _ := tm.Current()
	if !strings.HasSuffix(headers[1], cur.AccessToken) {
		t.Fatalf("restarted request should carry the refreshed token: %v vs %v", headers[1], cur.AccessToken)
	}
}

func TestOAuth2TransportRefreshFailureOn401(t *testing.T) {
	ft, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	ft.setFailRefresh(true)
	tm := NewTokenManager(cfg, TokenManagerOptions{})
	tm.SetToken(&OAuth2Token{AccessToken: "stale", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)})

	api, _ := newFakeDiscordAPI(t, "stale")
	defer api.Close()

	client := tm.HTTPClient(nil)
	_, err := client.Get(api.URL + "/x")
	if !errors.Is(err, ErrOAuth2RefreshAfter401) {
		t.Fatalf("expected ErrOAuth2RefreshAfter401, got %v", err)
	}
}

func TestOAuth2TransportNoTokenSource(t *testing.T) {
	_, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	tm := NewTokenManager(cfg, TokenManagerOptions{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()

	client := tm.HTTPClient(nil)
	if _, err := client.Get(srv.URL); !errors.Is(err, ErrOAuth2NoToken) {
		t.Fatalf("expected ErrOAuth2NoToken, got %v", err)
	}
}

// TestOAuth2AttachSession binds a TokenManager to a Session and checks
// that REST calls are authenticated with the user token and survive a 401
// restart.
func TestOAuth2AttachSession(t *testing.T) {
	_, cfg, cleanup := newFakeTokenEndpoint(t)
	defer cleanup()
	tm := NewTokenManager(cfg, TokenManagerOptions{})
	tm.SetToken(&OAuth2Token{AccessToken: "user-token", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)})

	var mu sync.Mutex
	var auths []string
	s, restCleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if strings.HasSuffix(r.Header.Get("Authorization"), "user-token") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "100", "username": "webuser"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer restCleanup()

	tm.AttachSession(s)
	if s.Token != "" {
		t.Fatalf("AttachSession should clear s.Token, got %q", s.Token)
	}

	u, err := s.User("@me")
	if err != nil {
		t.Fatalf("User(@me) failed: %v", err)
	}
	if u.Username != "webuser" || u.ID != "100" {
		t.Fatalf("unexpected user: %+v", u)
	}
	mu.Lock()
	if len(auths) == 0 || !strings.HasSuffix(auths[0], "Bearer user-token") {
		mu.Unlock()
		t.Fatalf("expected Bearer user-token header, got %v", auths)
	}
	mu.Unlock()
}

// ------------------------------------------------------------------------------------------------
// Session-bound OAuth2 REST helpers
// ------------------------------------------------------------------------------------------------

func TestSessionCurrentApplicationInfo(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/oauth2/applications/@me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id": "777", "name": "My App", "flags": 0}`))
	}))
	defer cleanup()

	app, err := s.CurrentApplicationInfo()
	if err != nil {
		t.Fatalf("CurrentApplicationInfo: %v", err)
	}
	if app.ID != "777" || app.Name != "My App" {
		t.Fatalf("unexpected application: %+v", app)
	}
}

func TestSessionOAuth2CurrentUserAndGuilds(t *testing.T) {
	var gotAuth string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/users/@me":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id": "500", "username": "u", "email": "u@example.com"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/@me/guilds":
			q := r.URL.Query()
			if q.Get("limit") != strconv.Itoa(7) || q.Get("with_counts") != "true" {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id": "g1", "name": "Guild One", "owner": true}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/users/@me/connections":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id": "c1", "name": "twitch", "type": "twitch"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer cleanup()

	user, err := s.OAuth2CurrentUser("tok-1")
	if err != nil {
		t.Fatalf("OAuth2CurrentUser: %v", err)
	}
	if user.ID != "500" || gotAuth != "Bearer tok-1" {
		t.Fatalf("unexpected user/auth: %+v / %q", user, gotAuth)
	}

	guilds, err := s.OAuth2CurrentUserGuilds("tok-1", 7, "", "a1", true)
	if err != nil {
		t.Fatalf("OAuth2CurrentUserGuilds: %v", err)
	}
	if len(guilds) != 1 || guilds[0].ID != "g1" {
		t.Fatalf("unexpected guilds: %+v", guilds)
	}

	conns, err := s.OAuth2CurrentUserConnections("tok-1")
	if err != nil {
		t.Fatalf("OAuth2CurrentUserConnections: %v", err)
	}
	if len(conns) != 1 || conns[0].Type != "twitch" {
		t.Fatalf("unexpected connections: %+v", conns)
	}
}

func TestSessionOAuth2CurrentUserGuildJoin(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/users/@me/guilds/g1" {
			http.NotFound(w, r)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bot ") {
			http.Error(w, "guilds.join must authenticate with the bot token", http.StatusUnauthorized)
			return
		}
		var body struct {
			AccessToken string `json:"access_token"`
			Nick        string `json:"nick"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.AccessToken != "user-tok" {
			http.Error(w, "bad access token", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": "g1", "name": "Guild One"}`))
	}))
	defer cleanup()

	g, err := s.OAuth2CurrentUserGuildJoin("user-tok", "g1", &GuildMemberAddParams{Nick: "joinee"})
	if err != nil {
		t.Fatalf("OAuth2CurrentUserGuildJoin: %v", err)
	}
	if g.ID != "g1" || g.Name != "Guild One" {
		t.Fatalf("unexpected guild: %+v", g)
	}
}
