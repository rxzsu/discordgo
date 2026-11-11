package discordgo

import "errors"

// One-line helpers for the common interaction responses, on top of
// InteractionRespond and FollowupMessageCreate. They construct the regular
// InteractionResponse / WebhookParams payloads and support the usual
// RequestOption arguments (WithContext, WithRetry, ...).
//
// Example:
//
//	switch i.Type {
//	case discordgo.InteractionApplicationCommand:
//		if err := s.ReplyEphemeral(i, "Working on it..."); err != nil {
//			return err
//		}
//		result := doSlowWork()
//		_, err = s.FollowUpEphemeral(i, "Done: "+result)
//	case discordgo.InteractionApplicationCommandAutocomplete:
//		err = s.ReplyAutocomplete(i, []*discordgo.ApplicationCommandOptionChoice{
//			{Name: "first", Value: "1"},
//		})
//	}

// ErrNoAutocompleteChoices is returned by ReplyAutocomplete when no choices
// are provided. Discord requires the choices field to be present, and the
// library's response struct omits an empty slice from the JSON payload, so
// an empty response cannot be expressed without producing an invalid
// request.
var ErrNoAutocompleteChoices = errors.New("discordgo: autocomplete response requires at least one choice")

// Reply responds to an interaction with a plain-text message
// (InteractionResponseChannelMessageWithSource).
func (s *Session) Reply(interaction *Interaction, content string, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseChannelMessageWithSource,
		Data: &InteractionResponseData{Content: content},
	}, options...)
}

// ReplyEphemeral responds to an interaction with a plain-text message only
// visible to the invoking user.
func (s *Session) ReplyEphemeral(interaction *Interaction, content string, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseChannelMessageWithSource,
		Data: &InteractionResponseData{Content: content, Flags: MessageFlagsEphemeral},
	}, options...)
}

// ReplyEmbeds responds to an interaction with one or more embeds.
func (s *Session) ReplyEmbeds(interaction *Interaction, embeds []*MessageEmbed, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseChannelMessageWithSource,
		Data: &InteractionResponseData{Embeds: embeds},
	}, options...)
}

// ReplyEmbedsEphemeral responds to an interaction with embeds only visible
// to the invoking user.
func (s *Session) ReplyEmbedsEphemeral(interaction *Interaction, embeds []*MessageEmbed, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseChannelMessageWithSource,
		Data: &InteractionResponseData{Embeds: embeds, Flags: MessageFlagsEphemeral},
	}, options...)
}

// ReplyComponents responds to an interaction with message components
// (buttons, select menus) built with the builders from
// builders_components.go, e.g. rows from NewRow().
func (s *Session) ReplyComponents(interaction *Interaction, components []MessageComponent, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseChannelMessageWithSource,
		Data: &InteractionResponseData{Components: components},
	}, options...)
}

// ReplyDeferred acknowledges an interaction with a "thinking..." state so a
// followup can be sent later with FollowUp / FollowUpData (application
// commands have three seconds to respond otherwise).
func (s *Session) ReplyDeferred(interaction *Interaction, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseDeferredChannelMessageWithSource,
	}, options...)
}

// ReplyDeferredEphemeral acknowledges an interaction with an ephemeral
// "thinking..." state; the followups answering it can then also be
// ephemeral.
func (s *Session) ReplyDeferredEphemeral(interaction *Interaction, options ...RequestOption) error {
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionResponseDeferredChannelMessageWithSource,
		Data: &InteractionResponseData{Flags: MessageFlagsEphemeral},
	}, options...)
}

// FollowUp sends a followup message for a (deferred) interaction and waits
// for the created message, e.g. after ReplyDeferred or ReplyDeferredEphemeral.
func (s *Session) FollowUp(interaction *Interaction, content string, options ...RequestOption) (*Message, error) {
	return s.FollowupMessageCreate(interaction, true, &WebhookParams{Content: content}, options...)
}

// FollowUpEphemeral sends an ephemeral followup message for a (deferred)
// interaction and waits for the created message.
func (s *Session) FollowUpEphemeral(interaction *Interaction, content string, options ...RequestOption) (*Message, error) {
	return s.FollowupMessageCreate(interaction, true, &WebhookParams{Content: content, Flags: MessageFlagsEphemeral}, options...)
}

// FollowUpData sends a followup message built from raw WebhookParams
// (embeds, components, files, ...) and waits for the created message.
func (s *Session) FollowUpData(interaction *Interaction, data *WebhookParams, options ...RequestOption) (*Message, error) {
	return s.FollowupMessageCreate(interaction, true, data, options...)
}

// ReplyAutocomplete answers an AUTOCOMPLETE interaction with the given
// choices. It returns ErrNoAutocompleteChoices when choices is empty.
func (s *Session) ReplyAutocomplete(interaction *Interaction, choices []*ApplicationCommandOptionChoice, options ...RequestOption) error {
	if len(choices) == 0 {
		return ErrNoAutocompleteChoices
	}
	return s.InteractionRespond(interaction, &InteractionResponse{
		Type: InteractionApplicationCommandAutocompleteResult,
		Data: &InteractionResponseData{Choices: choices},
	}, options...)
}

// ReplyModal responds to an interaction with a modal built with NewModal.
func (s *Session) ReplyModal(interaction *Interaction, modal *ModalBuilder, options ...RequestOption) error {
	return s.InteractionRespond(interaction, modal.Build(), options...)
}
