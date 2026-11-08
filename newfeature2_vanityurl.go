// This file implements the Guild Vanity URL REST API.
//
// A vanity URL is a custom invite code (discord.gg/<code>) for a guild with
// the VANITY_URL feature. Editing it requires the MANAGE_GUILD permission.
//
// https://discord.com/developers/docs/resources/guild#get-guild-vanity-url

package discordgo

import "errors"

// ErrEmptyGuildVanityCode is returned when an empty vanity code is passed to
// GuildVanityURLEdit. Discord requires a non-empty code; pass an existing
// code to keep it or use GuildVanityURL to read the current one.
var ErrEmptyGuildVanityCode = errors.New("guild vanity code must not be empty")

// GuildVanityURL is a guild's vanity invite code and its current usage.
// https://discord.com/developers/docs/resources/invite#invite-object
type GuildVanityURL struct {
	// The vanity invite code, empty when the guild has none.
	Code string `json:"code"`
	// Number of times the vanity invite has been used.
	Uses int `json:"uses"`
}

// GuildVanityURL returns the vanity invite code of a guild and the number of
// times it has been used. Requires the MANAGE_GUILD permission.
//
// guildID : The ID of the guild.
func (s *Session) GuildVanityURL(guildID string, options ...RequestOption) (vanity *GuildVanityURL, err error) {
	endpoint := EndpointGuildVanityURL(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &vanity)
	return
}

// GuildVanityURLEdit sets the vanity invite code of a guild and returns the
// updated GuildVanityURL. Requires the MANAGE_GUILD permission and the
// VANITY_URL guild feature.
//
// guildID : The ID of the guild.
// code    : The new vanity invite code.
func (s *Session) GuildVanityURLEdit(guildID, code string, options ...RequestOption) (vanity *GuildVanityURL, err error) {
	if code == "" {
		return nil, ErrEmptyGuildVanityCode
	}

	endpoint := EndpointGuildVanityURL(guildID)
	params := struct {
		Code string `json:"code"`
	}{Code: code}

	var body []byte
	body, err = s.RequestWithBucketID("PATCH", endpoint, params, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &vanity)
	return
}
