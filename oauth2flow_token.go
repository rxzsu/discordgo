// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file implements the OAuth2 authorization-code flow for Discord:
// building authorize URLs, exchanging an authorization code for a token
// and refreshing tokens. It is intentionally separate from oauth2.go,
// which only contains application CRUD, and only builds on the public
// surface of the package.

package discordgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ------------------------------------------------------------------------------------------------
// OAuth2 token representation
// ------------------------------------------------------------------------------------------------

// OAuth2Token is a Discord OAuth2 token as returned by the token endpoint.
// https://discord.com/developers/docs/topics/oauth2#authorization-code-grant-access-token-response
type OAuth2Token struct {
	// AccessToken is the bearer token used to call Discord endpoints on
	// behalf of the authorized user.
	AccessToken string `json:"access_token"`
	// TokenType is the token type, normally "Bearer".
	TokenType string `json:"token_type,omitempty"`
	// RefreshToken is used to obtain a new access token after this one
	// expires. Empty for tokens that cannot be refreshed.
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresIn is the token lifetime in seconds, as returned by Discord.
	ExpiresIn int64 `json:"expires_in,omitempty"`
	// Scope is the space-separated list of granted scopes.
	Scope string `json:"scope,omitempty"`
	// Expiry is the computed absolute expiration time. It is derived from
	// ExpiresIn when a token is received and persisted alongside the rest
	// of the token so a reloaded token knows when it needs refreshing.
	Expiry time.Time `json:"expiry,omitempty"`
	// Webhook is returned by Discord only when the webhook.incoming scope
	// is requested: it is the incoming webhook created for the app.
	Webhook *Webhook `json:"webhook,omitempty"`
}

// Expired reports whether the token has an Expiry in the past. Tokens
// without a computed Expiry are treated as never expiring.
func (t *OAuth2Token) Expired() bool {
	if t == nil || t.Expiry.IsZero() {
		return false
	}
	return !t.Expiry.After(time.Now())
}

// ExpiresWithin reports whether the token expires (or already expired)
// within d from now. Tokens without a computed Expiry never expire.
func (t *OAuth2Token) ExpiresWithin(d time.Duration) bool {
	if t == nil || t.Expiry.IsZero() {
		return false
	}
	return !t.Expiry.After(time.Now().Add(d))
}

// Valid reports whether the token carries an access token and has not
// expired.
func (t *OAuth2Token) Valid() bool {
	return t != nil && t.AccessToken != "" && !t.Expired()
}

// AuthHeader returns the value for an HTTP Authorization header that
// carries this token, e.g. "Bearer abc". An empty TokenType defaults to
// "Bearer", which is what Discord issues.
func (t *OAuth2Token) AuthHeader() string {
	if t == nil {
		return ""
	}
	typ := t.TokenType
	if typ == "" {
		typ = "Bearer"
	}
	return typ + " " + t.AccessToken
}

// clone returns a shallow copy so a snapshot can be handed out without
// exposing the stored pointer to mutation.
func (t *OAuth2Token) clone() *OAuth2Token {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

// ------------------------------------------------------------------------------------------------
// Client configuration and flow
// ------------------------------------------------------------------------------------------------

// Default URLs used by OAuth2Flow when the corresponding OAuth2ClientConfig
// field is empty. They are built from the public endpoint constants so a
// custom API base propagates automatically.
var (
	// OAuth2DefaultAuthURL is the authorization page shown to the user.
	OAuth2DefaultAuthURL = EndpointDiscord + "oauth2/authorize"
	// OAuth2DefaultTokenURL is the token endpoint that exchanges codes and
	// refresh tokens for access tokens.
	OAuth2DefaultTokenURL = EndpointOAuth2 + "token"
	// OAuth2DefaultUserAgent is sent with token endpoint requests.
	OAuth2DefaultUserAgent = "DiscordBot (https://github.com/rxzsu/discordgo, v" + VERSION + ")"
)

// OAuth2ClientConfig describes one OAuth2 client: the application's
// credentials plus the URLs and HTTP client used to talk to Discord.
type OAuth2ClientConfig struct {
	// ClientID is the application's ID (snowflake).
	ClientID string
	// ClientSecret is the application's OAuth2 secret.
	ClientSecret string
	// RedirectURI must match one of the redirect URIs registered for the
	// application.
	RedirectURI string
	// AuthURL overrides the authorization page URL. Empty means
	// OAuth2DefaultAuthURL.
	AuthURL string
	// TokenURL overrides the token endpoint. Empty means
	// OAuth2DefaultTokenURL.
	TokenURL string
	// HTTPClient is used for token endpoint requests. Empty means
	// http.DefaultClient. This client is used only for OAuth2 token
	// requests; it must not already carry an OAuth2Transport of this flow.
	HTTPClient *http.Client
}

// authURL returns the configured authorization page URL.
func (c OAuth2ClientConfig) authURL() string {
	if c.AuthURL != "" {
		return c.AuthURL
	}
	return OAuth2DefaultAuthURL
}

// tokenURL returns the configured token endpoint URL.
func (c OAuth2ClientConfig) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return OAuth2DefaultTokenURL
}

// httpClient returns the configured HTTP client.
func (c OAuth2ClientConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

// OAuth2AuthCodeParams are optional query parameters for an authorization
// URL. All fields are optional.
type OAuth2AuthCodeParams struct {
	// GuildID pre-selects a guild in the install UI (bot scope only).
	GuildID string
	// Permissions is the permissions bitfield requested with the bot scope.
	Permissions int64
	// DisableGuildSelect forbids the user from changing the pre-selected
	// guild.
	DisableGuildSelect bool
	// Prompt controls the consent screen: "consent" forces it, "none"
	// skips it (requires prior approval).
	Prompt string
	// IntegrationType restricts the install target, e.g. "1" for user
	// install and "0" for guild install.
	IntegrationType string
}

// OAuth2Error is returned when the token endpoint responds with a non-2xx
// status. Discord encodes OAuth2 failures as JSON with an "error" field.
type OAuth2Error struct {
	// StatusCode is the HTTP status code of the response.
	StatusCode int
	// Status is the HTTP status line.
	Status string
	// Body is the raw response body.
	Body []byte
	// ErrorCode is the OAuth2 error code from the JSON body, e.g.
	// "invalid_grant"; empty when the body could not be parsed.
	ErrorCode string
	// Description is the human readable error_description field, if any.
	Description string
}

// Error implements the error interface.
func (e *OAuth2Error) Error() string {
	if e.ErrorCode != "" {
		return fmt.Sprintf("oauth2: %s %d: %s: %s", e.Status, e.StatusCode, e.ErrorCode, e.Description)
	}
	return fmt.Sprintf("oauth2: %s %d: %s", e.Status, e.StatusCode, string(e.Body))
}

// sentinel errors returned by the flow and token manager.
var (
	// ErrOAuth2EmptyCode is returned when an authorization code is empty.
	ErrOAuth2EmptyCode = errors.New("oauth2: authorization code is empty")
	// ErrOAuth2EmptyRefreshToken is returned when a refresh is attempted
	// without a refresh token.
	ErrOAuth2EmptyRefreshToken = errors.New("oauth2: refresh token is empty")
	// ErrOAuth2InvalidTokenResponse is returned when the token endpoint
	// answers 2xx but the body carries no access token.
	ErrOAuth2InvalidTokenResponse = errors.New("oauth2: token response has no access_token")
	// ErrOAuth2NoToken is returned when a token is requested before any
	// token was set or exchanged.
	ErrOAuth2NoToken = errors.New("oauth2: no token available")
	// ErrOAuth2NoRefreshToken is returned when an expired token cannot be
	// refreshed because it carries no refresh token.
	ErrOAuth2NoRefreshToken = errors.New("oauth2: token has no refresh token")
	// ErrOAuth2TokenClosed is returned after the token manager was closed.
	ErrOAuth2TokenClosed = errors.New("oauth2: token manager is closed")
)

// OAuth2Flow implements the Discord authorization-code grant on top of an
// OAuth2ClientConfig. It is safe for concurrent use.
type OAuth2Flow struct {
	cfg OAuth2ClientConfig
}

// NewOAuth2Flow creates a flow for the given client configuration.
func NewOAuth2Flow(cfg OAuth2ClientConfig) *OAuth2Flow {
	return &OAuth2Flow{cfg: cfg}
}

// Config returns the flow's client configuration.
func (f *OAuth2Flow) Config() OAuth2ClientConfig {
	return f.cfg
}

// AuthCodeURL builds the URL a user must open to authorize the application.
//
//	state  : anti-CSRF token; Discord echoes it back as a query parameter.
//	scopes : requested OAuth2 scopes.
func (f *OAuth2Flow) AuthCodeURL(state string, scopes ...OAuth2Scope) string {
	return f.AuthCodeURLWithParams(state, OAuth2AuthCodeParams{}, scopes...)
}

// AuthCodeURLWithParams is AuthCodeURL with additional optional parameters.
func (f *OAuth2Flow) AuthCodeURLWithParams(state string, params OAuth2AuthCodeParams, scopes ...OAuth2Scope) string {
	v := url.Values{}
	v.Set("client_id", f.cfg.ClientID)
	v.Set("redirect_uri", f.cfg.RedirectURI)
	v.Set("response_type", "code")
	if s := JoinOAuth2Scopes(scopes...); s != "" {
		v.Set("scope", s)
	}
	if state != "" {
		v.Set("state", state)
	}
	if params.GuildID != "" {
		v.Set("guild_id", params.GuildID)
	}
	if params.Permissions != 0 {
		v.Set("permissions", strconv.FormatInt(params.Permissions, 10))
	}
	if params.DisableGuildSelect {
		v.Set("disable_guild_select", "true")
	}
	if params.Prompt != "" {
		v.Set("prompt", params.Prompt)
	}
	if params.IntegrationType != "" {
		v.Set("integration_type", params.IntegrationType)
	}
	return f.cfg.authURL() + "?" + v.Encode()
}

// ExchangeCode exchanges an authorization code for an OAuth2Token.
func (f *OAuth2Flow) ExchangeCode(ctx context.Context, code string) (*OAuth2Token, error) {
	if code == "" {
		return nil, ErrOAuth2EmptyCode
	}
	v := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {f.cfg.RedirectURI},
		"client_id":     {f.cfg.ClientID},
		"client_secret": {f.cfg.ClientSecret},
	}
	return f.tokenRequest(ctx, v)
}

// RefreshToken exchanges a refresh token for a fresh OAuth2Token.
func (f *OAuth2Flow) RefreshToken(ctx context.Context, refreshToken string) (*OAuth2Token, error) {
	if refreshToken == "" {
		return nil, ErrOAuth2EmptyRefreshToken
	}
	v := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {f.cfg.ClientID},
		"client_secret": {f.cfg.ClientSecret},
	}
	return f.tokenRequest(ctx, v)
}

// tokenRequest posts a form-encoded grant request to the token endpoint.
func (f *OAuth2Flow) tokenRequest(ctx context.Context, form url.Values) (*OAuth2Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", OAuth2DefaultUserAgent)

	resp, err := f.cfg.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("oauth2: reading token response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		oauthErr := &OAuth2Error{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       body,
		}
		var payload struct {
			ErrorCode   string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &payload) == nil {
			oauthErr.ErrorCode = payload.ErrorCode
			oauthErr.Description = payload.Description
		}
		return nil, oauthErr
	}

	var tok OAuth2Token
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("oauth2: decoding token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, ErrOAuth2InvalidTokenResponse
	}
	finalizeOAuth2Token(&tok)
	return &tok, nil
}

// finalizeOAuth2Token computes Expiry from ExpiresIn when the response
// carries a lifetime and the caller has not set Expiry itself.
func finalizeOAuth2Token(t *OAuth2Token) {
	if t.Expiry.IsZero() && t.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	}
}
