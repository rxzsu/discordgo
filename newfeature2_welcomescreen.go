// This file implements the Guild Welcome Screen REST API.
//
// The welcome screen shows a set of channels new members are shown in a
// community ("Client Onboarding") guild. It is only available for guilds
// with the WELCOME_SCREEN_ENABLED feature.
//
// https://discord.com/developers/docs/resources/guild#welcome-screen-object

package discordgo

import "errors"

// ErrNilWelcomeScreenParams is returned when a nil GuildWelcomeScreenParams
// is passed to GuildWelcomeScreenEdit.
var ErrNilWelcomeScreenParams = errors.New("welcome screen params must not be nil")

// WelcomeScreenChannel is a channel shown on a guild's welcome screen.
// https://discord.com/developers/docs/resources/guild#welcome-screen-object-welcome-screen-channel-structure
type WelcomeScreenChannel struct {
	// ID of the channel shown on the welcome screen.
	ChannelID string `json:"channel_id"`
	// Description shown for the channel (1-160 characters).
	Description string `json:"description"`
	// ID of a custom emoji shown next to the channel, empty for a
	// unicode emoji or no emoji.
	EmojiID string `json:"emoji_id"`
	// Name of a unicode emoji shown next to the channel, empty for a
	// custom emoji or no emoji.
	EmojiName string `json:"emoji_name"`
}

// WelcomeScreen is the welcome screen of a community guild.
// https://discord.com/developers/docs/resources/guild#welcome-screen-object
type WelcomeScreen struct {
	// Server description shown on the welcome screen, empty when unset.
	Description string `json:"description"`
	// Channels (up to 5) shown on the welcome screen.
	WelcomeChannels []WelcomeScreenChannel `json:"welcome_channels"`
}

// GuildWelcomeScreenParams stores the fields to update on a guild's welcome
// screen. Nil pointers and a nil WelcomeChannels slice are omitted from the
// request body, leaving the corresponding field unchanged.
// https://discord.com/developers/docs/resources/guild#modify-guild-welcome-screen
type GuildWelcomeScreenParams struct {
	// Whether the welcome screen is enabled.
	Enabled *bool `json:"enabled,omitempty"`
	// Server description shown on the welcome screen.
	Description *string `json:"description,omitempty"`
	// Channels (up to 5) shown on the welcome screen.
	WelcomeChannels []WelcomeScreenChannel `json:"welcome_channels,omitempty"`
}

// GuildWelcomeScreen returns the welcome screen of a guild. Requires the
// MANAGE_GUILD permission.
//
// guildID : The ID of the guild.
func (s *Session) GuildWelcomeScreen(guildID string, options ...RequestOption) (screen *WelcomeScreen, err error) {
	endpoint := EndpointGuildWelcomeScreen(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &screen)
	return
}

// GuildWelcomeScreenEdit updates the welcome screen of a guild and returns
// the updated WelcomeScreen. Requires the MANAGE_GUILD permission.
//
// guildID : The ID of the guild.
// params  : The fields to update. Nil fields are left unchanged.
func (s *Session) GuildWelcomeScreenEdit(guildID string, params *GuildWelcomeScreenParams, options ...RequestOption) (screen *WelcomeScreen, err error) {
	if params == nil {
		return nil, ErrNilWelcomeScreenParams
	}

	endpoint := EndpointGuildWelcomeScreen(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("PATCH", endpoint, params, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &screen)
	return
}
