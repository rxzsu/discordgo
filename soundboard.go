// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// This file contains REST methods for the guild widget and soundboard
// APIs.

package discordgo

// ------------------------------------------------------------------------------------------------
// Guild widget
// ------------------------------------------------------------------------------------------------

// GuildWidget returns the widget (embed) configuration and state of the
// given guild.
// guildID : The ID of a Guild.
func (s *Session) GuildWidget(guildID string, options ...RequestOption) (st *GuildWidget, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildWidgetJSON(guildID), nil, EndpointGuildWidgetJSON(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// GuildWidgetEdit edits the widget configuration of the given guild.
// Requires the MANAGE_GUILD permission.
// guildID : The ID of a Guild.
// p       : The widget parameters (enabled, channel_id).
func (s *Session) GuildWidgetEdit(guildID string, p *GuildWidgetEditParams, options ...RequestOption) (st *GuildWidget, err error) {
	body, err := s.RequestWithBucketID("PATCH", EndpointGuildWidgetJSON(guildID), p, EndpointGuildWidgetJSON(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &st)
	return
}

// ------------------------------------------------------------------------------------------------
// Soundboard
// ------------------------------------------------------------------------------------------------

// GuildSoundboardSounds returns the soundboard sounds of the given guild.
// guildID : The ID of a Guild.
func (s *Session) GuildSoundboardSounds(guildID string, options ...RequestOption) (sounds []*SoundboardSound, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildSoundboardSounds(guildID), nil, EndpointGuildSoundboardSounds(guildID), options...)
	if err != nil {
		return
	}

	soundList := struct {
		Items []*SoundboardSound `json:"items"`
	}{}
	err = unmarshal(body, &soundList)
	if err != nil {
		return
	}

	sounds = soundList.Items
	return
}

// GuildSoundboardSound returns the given soundboard sound of a guild.
// guildID  : The ID of a Guild.
// soundID  : The ID of a SoundboardSound.
func (s *Session) GuildSoundboardSound(guildID, soundID string, options ...RequestOption) (sound *SoundboardSound, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildSoundboardSound(guildID, soundID), nil, EndpointGuildSoundboardSound(guildID, soundID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &sound)
	return
}

// SoundboardSoundParams are the parameters for creating and editing
// soundboard sounds.
type SoundboardSoundParams struct {
	Name      string  `json:"name,omitempty"`
	Volume    float64 `json:"volume,omitempty"`
	EmojiID   string  `json:"emoji_id,omitempty"`
	EmojiName string  `json:"emoji_name,omitempty"`
}

// GuildSoundboardSoundCreate uploads a new soundboard sound to the given
// guild. The file must be an MP3 or OGG of at most 1 megabyte and at most
// 5.2 seconds long.
// guildID : The ID of a Guild.
// p       : The sound parameters (name, volume, emoji).
// file    : The sound file (MP3 or OGG).
func (s *Session) GuildSoundboardSoundCreate(guildID string, p *SoundboardSoundParams, file *File, options ...RequestOption) (sound *SoundboardSound, err error) {
	contentType, body, err := MultipartBodyWithJSON(p, []*File{file})
	if err != nil {
		return
	}

	response, err := s.RequestRaw("POST", EndpointGuildSoundboardSounds(guildID), contentType, body, EndpointGuildSoundboardSounds(guildID), 0, options...)
	if err != nil {
		return
	}

	err = unmarshal(response, &sound)
	return
}

// GuildSoundboardSoundEdit edits the given soundboard sound.
// guildID : The ID of a Guild.
// soundID : The ID of a SoundboardSound.
// p       : The sound parameters (name, volume, emoji).
func (s *Session) GuildSoundboardSoundEdit(guildID, soundID string, p *SoundboardSoundParams, options ...RequestOption) (sound *SoundboardSound, err error) {
	response, err := s.RequestWithBucketID("PATCH", EndpointGuildSoundboardSound(guildID, soundID), p, EndpointGuildSoundboardSound(guildID, soundID), options...)
	if err != nil {
		return
	}

	err = unmarshal(response, &sound)
	return
}

// GuildSoundboardSoundDelete deletes the given soundboard sound.
// guildID : The ID of a Guild.
// soundID : The ID of a SoundboardSound.
func (s *Session) GuildSoundboardSoundDelete(guildID, soundID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildSoundboardSound(guildID, soundID), nil, EndpointGuildSoundboardSound(guildID, soundID), options...)
	return
}

// ChannelSoundboardSoundSend plays the given soundboard sound in the
// given voice channel for everyone connected to it.
// channelID : The ID of a voice channel.
// soundID   : The ID of a SoundboardSound.
// guildID   : The ID of the guild the sound belongs to (required when
//
//	the sound comes from another guild).
func (s *Session) ChannelSoundboardSoundSend(channelID, soundID, guildID string, options ...RequestOption) (err error) {
	data := struct {
		SoundID string `json:"sound_id"`
		GuildID string `json:"guild_id,omitempty"`
	}{soundID, guildID}

	_, err = s.RequestWithBucketID("POST", EndpointChannelSendSoundboard(channelID), data, EndpointChannelSendSoundboard(channelID), options...)
	return
}
