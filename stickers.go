// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// This file contains REST methods for the guild sticker API.

package discordgo

// NitroStickerPacks returns the list of available nitro sticker packs.
func (s *Session) NitroStickerPacks(options ...RequestOption) (packs []*StickerPack, err error) {
	// NOTE: /stickers/ (the by-ID route) is not the packs endpoint;
	// the packs list lives at /sticker-packs.
	body, err := s.RequestWithBucketID("GET", EndpointNitroStickersPacks, nil, EndpointNitroStickersPacks, options...)
	if err != nil {
		return
	}

	packsList := struct {
		StickerPacks []*StickerPack `json:"sticker_packs"`
	}{}
	err = unmarshal(body, &packsList)
	if err != nil {
		return
	}

	packs = packsList.StickerPacks
	return
}

// GuildStickers returns the stickers of the given guild.
// guildID : The ID of a Guild.
func (s *Session) GuildStickers(guildID string, options ...RequestOption) (stickers []*Sticker, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildStickers(guildID), nil, EndpointGuildStickers(guildID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &stickers)
	return
}

// GuildSticker returns the given sticker of the given guild.
// guildID   : The ID of a Guild.
// stickerID : The ID of a Sticker.
func (s *Session) GuildSticker(guildID, stickerID string, options ...RequestOption) (sticker *Sticker, err error) {
	body, err := s.RequestWithBucketID("GET", EndpointGuildSticker(guildID, stickerID), nil, EndpointGuildSticker(guildID, stickerID), options...)
	if err != nil {
		return
	}

	err = unmarshal(body, &sticker)
	return
}

// GuildStickerCreate uploads a new sticker to the given guild.
// The file must be a PNG, APNG, or Lottie (JSON) file of at most
// 512 kilobytes.
// guildID   : The ID of a Guild.
// p         : Sticker parameters (name, description, tags).
// file      : The sticker file.
func (s *Session) GuildStickerCreate(guildID string, p *StickerParams, file *File, options ...RequestOption) (sticker *Sticker, err error) {
	contentType, body, err := MultipartBodyWithJSON(p, []*File{file})
	if err != nil {
		return
	}

	response, err := s.RequestRaw("POST", EndpointGuildStickers(guildID), contentType, body, EndpointGuildStickers(guildID), 0, options...)
	if err != nil {
		return
	}

	err = unmarshal(response, &sticker)
	return
}

// GuildStickerEdit edits the given sticker of the given guild.
// guildID   : The ID of a Guild.
// stickerID : The ID of a Sticker.
// p         : Sticker parameters (name, description, tags).
func (s *Session) GuildStickerEdit(guildID, stickerID string, p *StickerParams, options ...RequestOption) (sticker *Sticker, err error) {
	response, err := s.RequestWithBucketID("PATCH", EndpointGuildSticker(guildID, stickerID), p, EndpointGuildSticker(guildID, stickerID), options...)
	if err != nil {
		return
	}

	err = unmarshal(response, &sticker)
	return
}

// GuildStickerDelete deletes the given sticker of the given guild.
// guildID   : The ID of a Guild.
// stickerID : The ID of a Sticker.
func (s *Session) GuildStickerDelete(guildID, stickerID string, options ...RequestOption) (err error) {
	_, err = s.RequestWithBucketID("DELETE", EndpointGuildSticker(guildID, stickerID), nil, EndpointGuildSticker(guildID, stickerID), options...)
	return
}
