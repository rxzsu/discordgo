// This file implements the Guild Incident Actions REST API.
//
// Incident actions let moderators enable "raid panic mode": temporarily
// disable new invites and/or direct messages for all members of a guild.
// The current state is exposed by Discord as the "Incidents Data" object,
// which is also delivered through the GUILD_INCIDENTS_UPDATE gateway event.
//
// https://discord.com/developers/docs/resources/guild#incident-actions

package discordgo

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrNilIncidentActionsParams is returned when a nil
// GuildIncidentActionsParams is passed to GuildIncidentActionsEdit.
// Pass explicitly zero times inside the params to clear an action instead.
var ErrNilIncidentActionsParams = errors.New("incident actions params must not be nil")

// IncidentsData stores the moderation state of a guild that may be
// temporarily restricted during an incident (for example a raid).
// https://discord.com/developers/docs/resources/guild#incidents-data-object
type IncidentsData struct {
	// Until when new invites to the guild are paused, or nil when invites
	// are enabled again.
	InvitesDisabledUntil *time.Time `json:"invites_disabled_until"`
	// Until when direct messages from guild members are paused, or nil
	// when direct messages are enabled again.
	DMsDisabledUntil *time.Time `json:"dms_disabled_until"`
	// When Discord last detected direct message spam in the guild.
	DMSpamDetectedAt *time.Time `json:"dm_spam_detected_at,omitempty"`
	// When Discord last detected a raid in the guild.
	RaidDetectedAt *time.Time `json:"raid_detected_at,omitempty"`
}

// GuildIncidentActionsParams stores the incident actions to set on a guild.
//
// A nil timestamp pointer leaves the corresponding field out of the request
// body. A non-nil but zero time (time.Time{}) is sent as an explicit null,
// which lifts the restriction.
//
// Discord caps both windows at 31 days in the future.
// https://discord.com/developers/docs/resources/guild#incident-actions
type GuildIncidentActionsParams struct {
	// InvitesDisabledUntil pauses new guild invites until this moment.
	InvitesDisabledUntil *time.Time
	// DMsDisabledUntil pauses direct messages from guild members until
	// this moment.
	DMsDisabledUntil *time.Time
}

// MarshalJSON encodes the params, sending explicit nulls for zero times so
// restrictions can be lifted, and omitting nil timestamps entirely.
func (p GuildIncidentActionsParams) MarshalJSON() ([]byte, error) {
	v := struct {
		InvitesDisabledUntil json.RawMessage `json:"invites_disabled_until,omitempty"`
		DMsDisabledUntil     json.RawMessage `json:"dms_disabled_until,omitempty"`
	}{}

	if p.InvitesDisabledUntil != nil {
		raw, err := marshalTimestampOrClear(p.InvitesDisabledUntil)
		if err != nil {
			return nil, err
		}
		v.InvitesDisabledUntil = raw
	}

	if p.DMsDisabledUntil != nil {
		raw, err := marshalTimestampOrClear(p.DMsDisabledUntil)
		if err != nil {
			return nil, err
		}
		v.DMsDisabledUntil = raw
	}

	return Marshal(v)
}

// marshalTimestampOrClear encodes a nullable request timestamp: a zero time
// becomes an explicit JSON null, anything else is encoded as ISO 8601.
func marshalTimestampOrClear(t *time.Time) (json.RawMessage, error) {
	if t.IsZero() {
		return json.RawMessage(`null`), nil
	}
	raw, err := Marshal(t)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// GuildIncidentActionsEdit sets the incident actions of a guild, enabling or
// lifting temporary invite and direct message restrictions (raid panic
// mode). Requires the MANAGE_GUILD permission.
//
// guildID    : The ID of the guild.
// params     : The actions to set. Zero times inside the params clear the
//              corresponding restriction.
//
// Returns the updated IncidentsData of the guild.
func (s *Session) GuildIncidentActionsEdit(guildID string, params *GuildIncidentActionsParams, options ...RequestOption) (incidents *IncidentsData, err error) {
	if params == nil {
		return nil, ErrNilIncidentActionsParams
	}

	endpoint := EndpointGuildIncidentActions(guildID)

	var body []byte
	body, err = s.RequestWithBucketID("PUT", endpoint, params, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &incidents)
	return
}

// GuildWithIncidentsData returns a guild together with its current
// IncidentsData. Requires the MANAGE_GUILD permission for the incidents
// fields to be populated.
//
// guildID : The ID of the guild.
func (s *Session) GuildWithIncidentsData(guildID string, options ...RequestOption) (guild *GuildWithIncidentsData, err error) {
	endpoint := EndpointGuild(guildID) + "?with_incidents_data=true"

	var body []byte
	body, err = s.RequestWithBucketID("GET", endpoint, nil, BucketIDFromURL(endpoint), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &guild)
	return
}

// GuildWithIncidentsData wraps a Guild with the IncidentsData payload
// requested through the with_incidents_data query parameter.
type GuildWithIncidentsData struct {
	*Guild
	// IncidentsData is the current incident (raid panic) state of the
	// guild, or nil when it is not available.
	IncidentsData *IncidentsData `json:"incidents_data"`
}
