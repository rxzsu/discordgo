// This file implements the Guild Welcome Screen REST API.
//
// The welcome screen shows new members suggested channels in a community guild.
//
// https://discord.com/developers/docs/resources/guild#welcome-screen-object

package discordgo

import (
	"errors"
	"fmt"
)

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
	// Name of the custom emoji or the unicode character itself.
	// Empty when no name is set.
	EmojiName string `json:"emoji_name"`
}

// MarshalJSON sends absent emoji fields as null, as expected by Discord.
func (c WelcomeScreenChannel) MarshalJSON() ([]byte, error) {
	type welcomeChannel WelcomeScreenChannel
	wire := struct {
		welcomeChannel
		EmojiID   *string `json:"emoji_id"`
		EmojiName *string `json:"emoji_name"`
	}{welcomeChannel: welcomeChannel(c)}
	if c.EmojiID != "" {
		wire.EmojiID = &c.EmojiID
	}
	if c.EmojiName != "" {
		wire.EmojiName = &c.EmojiName
	}
	return Marshal(wire)
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
	// Channels (up to 5) shown on the welcome screen. An empty non-nil slice clears the list.
	WelcomeChannels []WelcomeScreenChannel `json:"welcome_channels,omitempty"`
}

// MarshalJSON distinguishes an omitted channel list from an empty list.
func (p GuildWelcomeScreenParams) MarshalJSON() ([]byte, error) {
	type welcomeParams GuildWelcomeScreenParams
	wire := struct {
		welcomeParams
		WelcomeChannels *[]WelcomeScreenChannel `json:"welcome_channels,omitempty"`
	}{welcomeParams: welcomeParams(p)}
	if p.WelcomeChannels != nil {
		wire.WelcomeChannels = &p.WelcomeChannels
	}
	return Marshal(wire)
}

// GuildWelcomeScreen returns the welcome screen of a guild. Requires the
// MANAGE_GUILD permission if the screen is disabled.
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
	if len(params.WelcomeChannels) > 5 {
		return nil, fmt.Errorf("a welcome screen supports at most 5 channels, got %d", len(params.WelcomeChannels))
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

// GuildWelcomeScreenEnable sets the description and enables the welcome screen
// of a community guild in one request. If channels is nil, the current list
// is left unchanged. Requires the MANAGE_GUILD permission.
func (s *Session) GuildWelcomeScreenEnable(guildID, description string, channels []WelcomeScreenChannel, options ...RequestOption) (*WelcomeScreen, error) {
	enabled := true
	return s.GuildWelcomeScreenEdit(guildID, &GuildWelcomeScreenParams{
		Enabled: &enabled, Description: &description, WelcomeChannels: channels,
	}, options...)
}
