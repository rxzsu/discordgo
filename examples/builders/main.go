// This example demonstrates the fluent builders (builders.go and
// builders_components.go) together with the compile-time type-safe event
// handlers from typed.go (AddHandlerTyped / AddHandlerOnceTyped).
//
// It sends a message assembled with NewMessage / NewEmbed / NewRow and the
// button / select-menu builders, then answers the component interactions
// with the one-line Reply* helpers from interaction_reply.go.
//
// Setup:
//  1. Set DISCORD_TOKEN to a bot token.
//  2. Optionally set DISCORD_DEMO_CHANNEL to a channel ID; the demo message
//     is sent there right after startup.
//  3. Type "!builders" in any channel the bot can see to (re)send the demo.
package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/rxzsu/discordgo"
)

// demoCommand triggers the demo message from any channel.
const demoCommand = "!builders"

func main() {
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN is not set")
	}
	demoChannel := os.Getenv("DISCORD_DEMO_CHANNEL")

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Invalid bot parameters: %v", err)
	}

	// Typed handlers: the event type is part of the compile-time signature,
	// so a handler can never be registered for the wrong event type (a
	// mistake AddHandler would only reveal at runtime - by never firing).
	discordgo.AddHandlerOnceTyped(s, onReady(demoChannel))
	discordgo.AddHandlerTyped(s, onMessageCreate)
	discordgo.AddHandlerTyped(s, onInteractionCreate)

	err = s.Open()
	if err != nil {
		log.Fatalf("Cannot open the session: %v", err)
	}
	defer s.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Println("Press Ctrl+C to exit")
	<-stop
	log.Println("Gracefully shutting down.")
}

// onReady posts the demo message once, when DISCORD_DEMO_CHANNEL is set.
func onReady(channelID string) func(*discordgo.Session, *discordgo.Ready) {
	return func(s *discordgo.Session, _ *discordgo.Ready) {
		if channelID == "" {
			log.Printf("Bot is up. Type %q in any channel to see the builders demo", demoCommand)
			return
		}
		sendDemoMessage(s, channelID)
	}
}

// onMessageCreate resends the demo message whenever someone types
// "!builders". Note the typed handler receives *MessageCreate directly.
func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}
	if m.Content != demoCommand {
		return
	}
	sendDemoMessage(s, m.ChannelID)
}

// onInteractionCreate answers component interactions with the Reply*
// helpers.
func onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	switch i.MessageComponentData().CustomID {
	case "builders:primary", "builders:success", "builders:danger":
		name := strings.TrimPrefix(i.MessageComponentData().CustomID, "builders:")
		err := s.ReplyEphemeral(i.Interaction, "You clicked the "+name+" button (New"+name+"Button helper).")
		if err != nil {
			log.Printf("Cannot reply: %v", err)
		}
	case "builders:select":
		// Answer with components built inline through ReplyComponents...
		row := discordgo.NewRow().
			AddButton(discordgo.NewSecondaryButton("Thanks!", "builders:thanks")).
			Build()
		if err := s.ReplyComponents(i.Interaction, []discordgo.MessageComponent{row}); err != nil {
			log.Printf("Cannot reply: %v", err)
			return
		}
		// ...then echo the chosen values as an ephemeral followup.
		values := strings.Join(i.MessageComponentData().Values, ", ")
		if _, err := s.FollowUpEphemeral(i.Interaction, "You picked: "+values); err != nil {
			log.Printf("Cannot send followup: %v", err)
		}
	case "builders:thanks":
		err := s.ReplyEphemeral(i.Interaction, "You are welcome!")
		if err != nil {
			log.Printf("Cannot reply: %v", err)
		}
	}
}

// sendDemoMessage sends the demo message built entirely with the fluent
// builders. MessageBuilder.Send builds the message and posts it with
// ChannelMessageSendComplex; the Build() result is a regular *MessageSend,
// so it can be passed to any existing API function as well.
func sendDemoMessage(s *discordgo.Session, channelID string) {
	msg, err := buildDemoMessage().Send(s, channelID)
	if err != nil {
		log.Printf("Cannot send builders demo: %v", err)
		return
	}
	log.Printf("Builders demo sent to channel %s (message %s)", channelID, msg.ID)
}

// buildDemoMessage assembles the demo message: an embed from EmbedBuilder,
// a row of colored buttons and a row with a multi-select menu.
func buildDemoMessage() *discordgo.MessageBuilder {
	embed := discordgo.NewEmbed().
		Title("discordgo builders").
		Description("This message was assembled with the fluent builders: NewMessage, NewEmbed, NewRow and friends.").
		Color(0x5865F2).
		Field("Buttons", "Click one - the interaction is answered with ReplyEphemeral / ReplyComponents.", true).
		Field("Select menu", "Pick your favorite languages (multi-select).", true).
		Footer("github.com/rxzsu/discordgo", "").
		Timestamp(time.Now()).
		Build()

	buttons := discordgo.NewRow().
		AddButton(discordgo.NewPrimaryButton("Primary", "builders:primary")).
		AddButton(discordgo.NewSuccessButton("Success", "builders:success")).
		AddButton(discordgo.NewDangerButton("Danger", "builders:danger")).
		AddButton(discordgo.NewLinkButton("Docs", "https://discord.com/developers/docs/interactions/message-components"))

	menu := discordgo.NewRow().
		AddMenu(discordgo.NewSelectMenu("builders:select").
			Placeholder("Favorite languages").
			MinValues(1).
			MaxValues(2).
			OptionDesc("Go", "go", "Simple yet powerful").
			OptionDesc("Rust", "rust", "Fearless concurrency").
			OptionDesc("Python", "python", "Quick to prototype"))

	return discordgo.NewMessage().
		Content("Builders demo:").
		Embed(embed).
		AddRow(buttons).
		AddRow(menu)
}
