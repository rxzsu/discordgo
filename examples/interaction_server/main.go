// This example runs a bot in Discord's "webhook interactions" mode: instead
// of keeping a gateway websocket connection open, an HTTP server receives
// interaction POSTs from Discord and answers them directly in the HTTP
// response. The bot works without any persistent connection to Discord.
//
// Setup:
//  1. Create an application in the Discord developer portal.
//  2. Copy the public key from the "General Information" page into the
//     INTERACTIONS_PUBLIC_KEY environment variable.
//  3. Set DISCORD_TOKEN to a bot token. It is only used for REST calls made
//     on top of received interactions (ReplyEphemeral below); the inline
//     responses need no token at all.
//  4. Expose the server through a public HTTPS URL (Discord requires HTTPS;
//     use a reverse proxy or a tunnel such as ngrok for local testing) and
//     set the "Interactions Endpoint URL" in the developer portal to
//     https://your.host/interactions. Discord verifies the URL by sending a
//     PING request, which the InteractionServer answers automatically.
//
// Optionally set DISCORD_APP_ID (and DISCORD_GUILD_ID for instant guild
// scope) to register the two slash commands of this example on startup.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/rxzsu/discordgo"
)

// The commands this example serves. They must be registered with Discord
// (see main) before users can invoke them.
var commands = []*discordgo.ApplicationCommand{
	{
		Name:        "hello",
		Description: "Say hello (ephemeral reply sent over REST)",
	},
	{
		Name:        "panel",
		Description: "Show a panel of buttons built with the builders",
	},
}

func main() {
	publicKeyHex := os.Getenv("INTERACTIONS_PUBLIC_KEY")
	token := os.Getenv("DISCORD_TOKEN")
	if publicKeyHex == "" {
		log.Fatal("INTERACTIONS_PUBLIC_KEY is not set (copy the public key from the developer portal)")
	}
	if token == "" {
		log.Fatal("DISCORD_TOKEN is not set")
	}

	publicKey, err := discordgo.InteractionPublicKeyFromHex(publicKeyHex)
	if err != nil {
		log.Fatalf("Invalid INTERACTIONS_PUBLIC_KEY: %v", err)
	}

	// A regular session built from the bot token. It is deliberately NOT
	// opened (no Session.Open call): in webhook mode there is no gateway
	// connection. The session is only used for REST helpers such as
	// Reply / ReplyEphemeral / FollowUp, called from interaction handlers.
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatalf("Invalid bot parameters: %v", err)
	}

	// Optionally register the slash commands. Guild-scoped commands appear
	// instantly, global ones may take up to an hour to propagate.
	appID := os.Getenv("DISCORD_APP_ID")
	guildID := os.Getenv("DISCORD_GUILD_ID")
	if appID != "" {
		for _, cmd := range commands {
			if _, err = s.ApplicationCommandCreate(appID, guildID, cmd); err != nil {
				log.Fatalf("Cannot create '%v' command: %v", cmd.Name, err)
			}
		}
	} else {
		log.Println("DISCORD_APP_ID not set: skipping command registration (register /hello and /panel manually)")
	}

	// InteractionServer is a plain http.Handler: it verifies the Ed25519
	// signature of every request, answers the PING handshake and dispatches
	// interactions to the registered handlers.
	is := discordgo.NewInteractionServer(publicKey).
		OnApplicationCommand(onApplicationCommand(s)).
		OnComponent(onComponent)

	mux := http.NewServeMux()
	mux.Handle("/interactions", is)

	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Println("Interaction server listening on :8080 (POST /interactions)")
		log.Println(`Set "Interactions Endpoint URL" to https://<public-host>/interactions in the developer portal`)
		if err = httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Println("Press Ctrl+C to exit")
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = httpServer.Shutdown(ctx); err != nil {
		log.Printf("HTTP server shutdown: %v", err)
	}
	log.Println("Gracefully shutting down.")
}

// onApplicationCommand dispatches slash commands by name. A handler either
// returns an *InteractionResponse, which is written straight to the HTTP
// body, or nil when the response is sent over REST instead (the server then
// answers 204 No Content and Discord accepts the REST callback as the reply,
// as long as it happens within three seconds).
func onApplicationCommand(s *discordgo.Session) discordgo.InteractionHandler {
	return func(i *discordgo.Interaction) *discordgo.InteractionResponse {
		switch i.ApplicationCommandData().Name {
		case "hello":
			// REST pattern: ReplyEphemeral sends the response through the
			// REST API, so the HTTP handler returns nil.
			greeting := "Hey there, " + userName(i) + "! This reply was sent with s.ReplyEphemeral over REST."
			if err := s.ReplyEphemeral(i, greeting); err != nil {
				log.Printf("Cannot send REST reply: %v", err)
			}
			return nil
		case "panel":
			// Inline pattern: rows and buttons are built with the fluent
			// builders and returned as the HTTP response body.
			return &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Pick a button, any button:",
					Components: []discordgo.MessageComponent{
						discordgo.NewRow().
							AddButton(discordgo.NewPrimaryButton("Say hi", "panel:hi")).
							AddButton(discordgo.NewDangerButton("Boom", "panel:boom")).
							Build(),
						discordgo.NewRow().
							AddButton(discordgo.NewLinkButton("Discord docs", "https://discord.com/developers/docs")).
							Build(),
					},
				},
			}
		}
		return nil
	}
}

// onComponent answers button clicks inline with responses built from the
// same builders.
func onComponent(i *discordgo.Interaction) *discordgo.InteractionResponse {
	switch i.MessageComponentData().CustomID {
	case "panel:hi":
		return ephemeral("Hey again, " + userName(i) + "! This button was built with NewPrimaryButton.")
	case "panel:boom":
		return ephemeral("Boom! 💥 That was NewDangerButton.")
	}
	return nil
}

// ephemeral builds a small inline ephemeral response.
func ephemeral(content string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

// userName returns the name of the invoking user. In guilds it arrives in
// Member.User, in DMs in User.
func userName(i *discordgo.Interaction) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.Username
	}
	if i.User != nil {
		return i.User.Username
	}
	return "stranger"
}
