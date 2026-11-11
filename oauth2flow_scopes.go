// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file holds OAuth2 scope helpers and Session-bound REST helpers for
// endpoints that are called with a user OAuth2 token (user-installed apps
// and web applications). oauth2.go is not modified by any of this.

package discordgo

import (
	"net/url"
	"strconv"
	"strings"
)

// ------------------------------------------------------------------------------------------------
// Scopes
// ------------------------------------------------------------------------------------------------

// OAuth2Scope is a Discord OAuth2 scope.
// https://discord.com/developers/docs/topics/oauth2#scoped-oauth2-scopes
type OAuth2Scope string

// The OAuth2 scopes documented by Discord.
const (
	// OAuth2ScopeActivitiesRead allows reading activities a user is in.
	OAuth2ScopeActivitiesRead OAuth2Scope = "activities.read"
	// OAuth2ScopeActivitiesWrite allows updating activities a user is in.
	OAuth2ScopeActivitiesWrite OAuth2Scope = "activities.write"
	// OAuth2ScopeApplicationsBuilds allows reading build data for the
	// user's applications.
	OAuth2ScopeApplicationsBuilds OAuth2Scope = "applications.builds"
	// OAuth2ScopeApplicationsBuildsUpload allows uploading builds.
	OAuth2ScopeApplicationsBuildsUpload OAuth2Scope = "applications.builds.upload"
	// OAuth2ScopeApplicationsEntitlements allows reading entitlements for
	// the user's applications.
	OAuth2ScopeApplicationsEntitlements OAuth2Scope = "applications.entitlements"
	// OAuth2ScopeApplicationsStoreUpdate allows updating store data.
	OAuth2ScopeApplicationsStoreUpdate OAuth2Scope = "applications.store.update"
	// OAuth2ScopeBot adds the bot to the selected guild.
	OAuth2ScopeBot OAuth2Scope = "bot"
	// OAuth2ScopeConnections allows reading the user's third-party
	// connections.
	OAuth2ScopeConnections OAuth2Scope = "connections"
	// OAuth2ScopeDMChannelsRead allows reading DM channels the user is in.
	OAuth2ScopeDMChannelsRead OAuth2Scope = "dm_channels.read"
	// OAuth2ScopeEmail allows reading the user's email address.
	OAuth2ScopeEmail OAuth2Scope = "email"
	// OAuth2ScopeGuilds allows reading the guilds the user is in.
	OAuth2ScopeGuilds OAuth2Scope = "guilds"
	// OAuth2ScopeGuildsJoin allows the bot to add the user to guilds.
	OAuth2ScopeGuildsJoin OAuth2Scope = "guilds.join"
	// OAuth2ScopeGuildsDriversRead allows reading the user's guild
	// driver information.
	OAuth2ScopeGuildsDriversRead OAuth2Scope = "guilds.drivers.read"
	// OAuth2ScopeIdentify allows reading the user's identity.
	OAuth2ScopeIdentify OAuth2Scope = "identify"
	// OAuth2ScopeMessagesRead allows reading messages the user can see in
	// guilds the app is in.
	OAuth2ScopeMessagesRead OAuth2Scope = "messages.read"
	// OAuth2ScopeRelationshipsRead allows reading the user's friend list.
	OAuth2ScopeRelationshipsRead OAuth2Scope = "relationships.read"
	// OAuth2ScopeRPC allows RPC over the local RPC socket.
	OAuth2ScopeRPC OAuth2Scope = "rpc"
	// OAuth2ScopeRPCActivitiesWrite allows updating the user's activity
	// over RPC.
	OAuth2ScopeRPCActivitiesWrite OAuth2Scope = "rpc.activities.write"
	// OAuth2ScopeRPCNotificationsRead allows reading notification state
	// over RPC.
	OAuth2ScopeRPCNotificationsRead OAuth2Scope = "rpc.notifications.read"
	// OAuth2ScopeRPCVoiceRead allows reading voice state over RPC.
	OAuth2ScopeRPCVoiceRead OAuth2Scope = "rpc.voice.read"
	// OAuth2ScopeRPCVoiceWrite allows mutating voice state over RPC.
	OAuth2ScopeRPCVoiceWrite OAuth2Scope = "rpc.voice.write"
	// OAuth2ScopeVoice allows connecting to voice (client-side).
	OAuth2ScopeVoice OAuth2Scope = "voice"
	// OAuth2ScopeWebhookIncoming creates an incoming webhook owned by the
	// app and returns it in the token response.
	OAuth2ScopeWebhookIncoming OAuth2Scope = "webhook.incoming"
)

// String returns the wire representation of the scope.
func (s OAuth2Scope) String() string {
	return string(s)
}

// JoinOAuth2Scopes joins scopes into the space-separated form used in
// authorize URLs and token responses.
func JoinOAuth2Scopes(scopes ...OAuth2Scope) string {
	parts := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if s == "" {
			continue
		}
		parts = append(parts, string(s))
	}
	return strings.Join(parts, " ")
}

// ParseOAuth2Scopes splits a space-separated scope string (authorize URL
// parameter, token response "scope" field) into scopes. Duplicates are
// dropped and order is not preserved.
func ParseOAuth2Scopes(s string) []OAuth2Scope {
	fields := strings.Fields(s)
	seen := make(map[OAuth2Scope]struct{}, len(fields))
	out := make([]OAuth2Scope, 0, len(fields))
	for _, f := range fields {
		sc := OAuth2Scope(f)
		if _, dup := seen[sc]; dup {
			continue
		}
		seen[sc] = struct{}{}
		out = append(out, sc)
	}
	return out
}

// HasOAuth2Scope reports whether the scope list contains want.
func HasOAuth2Scope(scopes []OAuth2Scope, want OAuth2Scope) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// HasScope reports whether the token was granted want.
func (t *OAuth2Token) HasScope(want OAuth2Scope) bool {
	if t == nil {
		return false
	}
	return HasOAuth2Scope(ParseOAuth2Scopes(t.Scope), want)
}

// OAuth2ScopesIdentification returns the minimal scope set for web apps
// that only need to know who the user is.
func OAuth2ScopesIdentification() []OAuth2Scope {
	return []OAuth2Scope{OAuth2ScopeIdentify}
}

// OAuth2ScopesIdentificationEmail returns scopes for identifying the user
// and reading their e-mail address.
func OAuth2ScopesIdentificationEmail() []OAuth2Scope {
	return []OAuth2Scope{OAuth2ScopeIdentify, OAuth2ScopeEmail}
}

// OAuth2ScopesGuildsJoin returns the scopes needed to add the user to
// guilds on their behalf.
func OAuth2ScopesGuildsJoin() []OAuth2Scope {
	return []OAuth2Scope{OAuth2ScopeIdentify, OAuth2ScopeGuilds, OAuth2ScopeGuildsJoin}
}

// OAuth2ScopesConnections returns the scopes for reading the user's
// third-party connections.
func OAuth2ScopesConnections() []OAuth2Scope {
	return []OAuth2Scope{OAuth2ScopeIdentify, OAuth2ScopeConnections}
}

// ------------------------------------------------------------------------------------------------
// Session-bound OAuth2 REST helpers
// ------------------------------------------------------------------------------------------------

// CurrentApplicationInfo returns the application that owns the session's
// credentials, by calling GET /oauth2/applications/@me. Unlike the CRUD
// helpers in oauth2.go it does not need an application ID and works with
// bot tokens and user tokens alike.
func (s *Session) CurrentApplicationInfo(options ...RequestOption) (st *Application, err error) {
	endpoint := EndpointOAuth2 + "applications/@me"
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint, options...)
	if err != nil {
		return
	}
	err = unmarshal(body, &st)
	return
}

// OAuth2CurrentUser returns the user the given access token belongs to,
// by calling GET /users/@me with the token in the Authorization header.
// Requires the identify scope (and email for the e-mail field).
func (s *Session) OAuth2CurrentUser(accessToken string, options ...RequestOption) (st *User, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointUser("@me"), nil, EndpointUsers,
		append(options, WithBearerToken(accessToken))...)
	if err != nil {
		return
	}
	err = unmarshal(body, &st)
	return
}

// OAuth2CurrentUserGuilds returns the guilds the token's user is in, by
// calling GET /users/@me/guilds with the token. Requires the guilds scope.
func (s *Session) OAuth2CurrentUserGuilds(accessToken string, limit int, beforeID, afterID string, withCounts bool, options ...RequestOption) (st []*UserGuild, err error) {
	uri := EndpointUserGuilds("@me")
	v := urlValuesForOAuth2UserGuilds(limit, beforeID, afterID, withCounts)
	if encoded := v.Encode(); encoded != "" {
		uri += "?" + encoded
	}
	body, err := s.RequestWithBucketID("GET", uri, nil, EndpointUserGuilds(""),
		append(options, WithBearerToken(accessToken))...)
	if err != nil {
		return
	}
	err = unmarshal(body, &st)
	return
}

// urlValuesForOAuth2UserGuilds builds the query parameters of the
// /users/@me/guilds endpoint, mirroring Session.UserGuilds.
func urlValuesForOAuth2UserGuilds(limit int, beforeID, afterID string, withCounts bool) url.Values {
	v := url.Values{}
	if limit > 0 {
		v.Set("limit", strconv.Itoa(limit))
	}
	if beforeID != "" {
		v.Set("before", beforeID)
	}
	if afterID != "" {
		v.Set("after", afterID)
	}
	if withCounts {
		v.Set("with_counts", "true")
	}
	return v
}

// OAuth2CurrentUserConnections returns the third-party connections of the
// token's user, by calling GET /users/@me/connections with the token.
// Requires the connections scope.
func (s *Session) OAuth2CurrentUserConnections(accessToken string, options ...RequestOption) (st []*UserConnection, err error) {
	endpoint := EndpointUserConnections("@me")
	body, err := s.RequestWithBucketID("GET", endpoint, nil, endpoint,
		append(options, WithBearerToken(accessToken))...)
	if err != nil {
		return
	}
	err = unmarshal(body, &st)
	return
}

// OAuth2CurrentUserGuildJoin adds the token's user to the given guild,
// by calling PUT /users/@me/guilds/{guild.id}. Authorization uses the
// session's own token (a bot token) while the user token is passed in the
// request body, exactly as the endpoint requires. Requires the
// guilds.join scope on the user token.
//
//	params may carry a nickname and initial role assignments; it may be
//	nil.
//
// The endpoint answers with the joined Guild.
func (s *Session) OAuth2CurrentUserGuildJoin(accessToken, guildID string, params *GuildMemberAddParams, options ...RequestOption) (st *Guild, err error) {
	if params == nil {
		params = &GuildMemberAddParams{}
	}
	// The body must carry the user's access token, never a stale one.
	params.AccessToken = accessToken
	body, err := s.RequestWithBucketID("PUT", EndpointUserGuild("@me", guildID), params, EndpointUserGuild("", guildID), options...)
	if err != nil {
		return
	}
	err = unmarshal(body, &st)
	return
}

// WithBearerToken overrides the Authorization header of a request with the
// given bearer token. It is the escape hatch used by the OAuth2CurrentUser*
// helpers to authenticate with a user OAuth2 token instead of the session
// token; it can also be used directly.
func WithBearerToken(token string) RequestOption {
	return WithHeader("Authorization", "Bearer "+token)
}
