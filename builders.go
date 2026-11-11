package discordgo

import (
	"io"
	"time"
)

// Fluent builders for message payloads and embeds. They construct the
// existing library structs (MessageSend, MessageEmbed) and introduce no new
// wire types: Build() results are passed straight into the regular REST
// functions (ChannelMessageSendComplex, InteractionRespond, ...).

// EmbedBuilder fluently constructs a *MessageEmbed. Example:
//
//	embed := discordgo.NewEmbed().
//		Title("Release notes").
//		Description("v1.7.0 is out").
//		Color(0x5865F2).
//		Field("Highlights", "- typed handlers\n- builders", false).
//		Build()
type EmbedBuilder struct {
	embed MessageEmbed
}

// NewEmbed returns an empty EmbedBuilder.
func NewEmbed() *EmbedBuilder {
	return &EmbedBuilder{}
}

// Title sets the embed title.
func (b *EmbedBuilder) Title(title string) *EmbedBuilder {
	b.embed.Title = title
	return b
}

// Description sets the embed description.
func (b *EmbedBuilder) Description(description string) *EmbedBuilder {
	b.embed.Description = description
	return b
}

// URL sets the embed URL (the title becomes a hyperlink).
func (b *EmbedBuilder) URL(url string) *EmbedBuilder {
	b.embed.URL = url
	return b
}

// Color sets the embed side-bar color as an RGB integer, e.g. 0x5865F2.
func (b *EmbedBuilder) Color(color int) *EmbedBuilder {
	b.embed.Color = color
	return b
}

// Timestamp sets the embed timestamp; the zero time leaves it unset.
func (b *EmbedBuilder) Timestamp(t time.Time) *EmbedBuilder {
	if !t.IsZero() {
		b.embed.Timestamp = t.Format(time.RFC3339)
	}
	return b
}

// Author sets the embed author.
func (b *EmbedBuilder) Author(name, iconURL, url string) *EmbedBuilder {
	b.embed.Author = &MessageEmbedAuthor{Name: name, IconURL: iconURL, URL: url}
	return b
}

// Footer sets the embed footer.
func (b *EmbedBuilder) Footer(text, iconURL string) *EmbedBuilder {
	b.embed.Footer = &MessageEmbedFooter{Text: text, IconURL: iconURL}
	return b
}

// Image sets the large image of the embed.
func (b *EmbedBuilder) Image(url string) *EmbedBuilder {
	b.embed.Image = &MessageEmbedImage{URL: url}
	return b
}

// Thumbnail sets the small thumbnail image of the embed.
func (b *EmbedBuilder) Thumbnail(url string) *EmbedBuilder {
	b.embed.Thumbnail = &MessageEmbedThumbnail{URL: url}
	return b
}

// Field appends a field; inline fields flow next to each other. Discord
// allows at most 25 fields per embed.
func (b *EmbedBuilder) Field(name, value string, inline bool) *EmbedBuilder {
	b.embed.Fields = append(b.embed.Fields, &MessageEmbedField{Name: name, Value: value, Inline: inline})
	return b
}

// Fields appends pre-built fields.
func (b *EmbedBuilder) Fields(fields ...*MessageEmbedField) *EmbedBuilder {
	b.embed.Fields = append(b.embed.Fields, fields...)
	return b
}

// Build returns the constructed embed. The builder remains reusable.
func (b *EmbedBuilder) Build() *MessageEmbed {
	embed := b.embed
	return &embed
}

// MessageBuilder fluently constructs a *MessageSend for
// ChannelMessageSendComplex (and, via Build(), for any other API taking
// message content). Example:
//
//	msg, err := discordgo.NewMessage().
//		Content("New release!").
//		Embeds(embeds).
//		AddRow(discordgo.NewRow().
//			AddButton(discordgo.NewPrimaryButton("docs", "open-docs"))).
//		Send(s, channelID)
type MessageBuilder struct {
	send MessageSend
}

// NewMessage returns an empty MessageBuilder.
func NewMessage() *MessageBuilder {
	return &MessageBuilder{}
}

// Content sets the plain-text content of the message.
func (b *MessageBuilder) Content(content string) *MessageBuilder {
	b.send.Content = content
	return b
}

// Embed sets a single embed, replacing any previously added ones.
func (b *MessageBuilder) Embed(embed *MessageEmbed) *MessageBuilder {
	b.send.Embeds = []*MessageEmbed{embed}
	return b
}

// Embeds sets the embeds of the message, replacing any previously added
// ones. Discord allows at most 10 embeds per message.
func (b *MessageBuilder) Embeds(embeds []*MessageEmbed) *MessageBuilder {
	b.send.Embeds = embeds
	return b
}

// AddEmbed appends one embed.
func (b *MessageBuilder) AddEmbed(embed *MessageEmbed) *MessageBuilder {
	b.send.Embeds = append(b.send.Embeds, embed)
	return b
}

// AddRow appends a component row built with NewRow.
func (b *MessageBuilder) AddRow(row *RowBuilder) *MessageBuilder {
	row_ := row.Build()
	b.send.Components = append(b.send.Components, &row_)
	return b
}

// AddComponent appends a raw component (power users).
func (b *MessageBuilder) AddComponent(component MessageComponent) *MessageBuilder {
	b.send.Components = append(b.send.Components, component)
	return b
}

// Components sets the components of the message, replacing any previously
// added ones.
func (b *MessageBuilder) Components(components []MessageComponent) *MessageBuilder {
	b.send.Components = components
	return b
}

// TTS enables text-to-speech.
func (b *MessageBuilder) TTS(tts bool) *MessageBuilder {
	b.send.TTS = tts
	return b
}

// AddFile appends a file attachment.
func (b *MessageBuilder) AddFile(name string, reader io.Reader) *MessageBuilder {
	b.send.Files = append(b.send.Files, &File{Name: name, Reader: reader})
	return b
}

// AllowedMentions restricts which mentions Discord will parse.
func (b *MessageBuilder) AllowedMentions(allowed *MessageAllowedMentions) *MessageBuilder {
	b.send.AllowedMentions = allowed
	return b
}

// ReplyTo makes the message a reply to the given message in the same
// channel. With the default reference type, sending fails if the referenced
// message no longer exists.
func (b *MessageBuilder) ReplyTo(channelID, messageID string) *MessageBuilder {
	b.send.Reference = &MessageReference{
		Type:      MessageReferenceTypeDefault,
		ChannelID: channelID,
		MessageID: messageID,
	}
	return b
}

// StickerIDs attaches stickers by ID.
func (b *MessageBuilder) StickerIDs(ids ...string) *MessageBuilder {
	b.send.StickerIDs = append(b.send.StickerIDs, ids...)
	return b
}

// Flags sets message flags (e.g. MessageFlagsSuppressEmbeds).
func (b *MessageBuilder) Flags(flags MessageFlags) *MessageBuilder {
	b.send.Flags = flags
	return b
}

// Poll attaches a poll.
func (b *MessageBuilder) Poll(poll *Poll) *MessageBuilder {
	b.send.Poll = poll
	return b
}

// Build returns the constructed MessageSend. The builder remains reusable.
func (b *MessageBuilder) Build() *MessageSend {
	send := b.send
	return &send
}

// Send posts the built message to the given channel with
// ChannelMessageSendComplex and returns the resulting message.
func (b *MessageBuilder) Send(s *Session, channelID string, options ...RequestOption) (*Message, error) {
	return s.ChannelMessageSendComplex(channelID, b.Build(), options...)
}
