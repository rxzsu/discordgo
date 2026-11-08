// This file implements partial updates of the Guild Onboarding API.
//
// The full onboarding edit (GuildOnboardingEdit in restapi.go) requires every
// onboarding field to be sent. Discord additionally supports a partial update
// via PUT /guilds/{id}/onboarding?partial=true, where only the prompts or
// only the enabled flag is required. This is useful to flip onboarding on or
// off without rewriting the whole prompt flow.
//
// https://discord.com/developers/docs/resources/guild#modify-guild-onboarding

package discordgo

import "errors"

var (
	// ErrNilOnboardingPartialEditParams is returned when a nil
	// GuildOnboardingPartialEditParams is passed to
	// GuildOnboardingEditPartial.
	ErrNilOnboardingPartialEditParams = errors.New("onboarding partial edit params must not be nil")

	// ErrEmptyOnboardingPartialEditParams is returned when none of the
	// fields of GuildOnboardingPartialEditParams are set, which would
	// produce an empty partial update.
	ErrEmptyOnboardingPartialEditParams = errors.New("at least one onboarding partial edit field must be set")
)

// GuildOnboardingPartialEditParams stores the onboarding fields to update in
// a partial onboarding edit. Nil fields are omitted from the request body,
// leaving the corresponding part of the onboarding flow unchanged.
//
// With partial=true Discord only requires one group of fields (prompts or
// enabled or default_channel_ids), so any single field may be updated alone.
type GuildOnboardingPartialEditParams struct {
	// Prompts shown during onboarding and in the customize community
	// (Channels & Roles) tab.
	Prompts []*GuildOnboardingPrompt
	// Channel IDs that members get opted into automatically.
	DefaultChannelIDs []string
	// Whether onboarding is enabled in the guild.
	Enabled *bool
	// Mode of onboarding.
	Mode *GuildOnboardingMode
}

// empty reports whether no field of the params is set.
func (p *GuildOnboardingPartialEditParams) empty() bool {
	return p.Prompts == nil && p.DefaultChannelIDs == nil && p.Enabled == nil && p.Mode == nil
}

// GuildOnboardingEditPartial updates parts of a guild's onboarding
// configuration via the partial onboarding update (?partial=true) and
// returns the updated GuildOnboarding. Unlike GuildOnboardingEdit, only the
// fields set in params are sent; everything else is left unchanged.
// Requires the MANAGE_GUILD permission.
//
// guildID : The ID of the guild.
// params  : The fields to update. At least one field must be set.
func (s *Session) GuildOnboardingEditPartial(guildID string, params *GuildOnboardingPartialEditParams, options ...RequestOption) (onboarding *GuildOnboarding, err error) {
	if params == nil {
		return nil, ErrNilOnboardingPartialEditParams
	}
	if params.empty() {
		return nil, ErrEmptyOnboardingPartialEditParams
	}

	endpoint := EndpointGuildOnboarding(guildID) + "?partial=true"

	// A non-nil Prompts slice is always sent, even when empty, so an
	// explicit empty list can clear all prompts; a nil slice is omitted.
	var prompts *[]*GuildOnboardingPrompt
	if params.Prompts != nil {
		p := params.Prompts
		prompts = &p
	}

	body := struct {
		Prompts           *[]*GuildOnboardingPrompt `json:"prompts,omitempty"`
		DefaultChannelIDs []string                  `json:"default_channel_ids,omitempty"`
		Enabled           *bool                     `json:"enabled,omitempty"`
		Mode              *GuildOnboardingMode      `json:"mode,omitempty"`
	}{
		Prompts:           prompts,
		DefaultChannelIDs: params.DefaultChannelIDs,
		Enabled:           params.Enabled,
		Mode:              params.Mode,
	}

	var raw []byte
	raw, err = s.RequestWithBucketID("PUT", endpoint, body, EndpointGuildOnboarding(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(raw, &onboarding)
	return
}
