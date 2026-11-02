// This example demonstrates using the ShardManager to connect a bot
// across multiple gateway shards with shared event handlers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rxzsu/discordgo"
)

func main() {
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		fmt.Println("DISCORD_TOKEN environment variable is required")
		return
	}

	// One handler, registered on the manager: it fires for events from
	// every shard.
	m := discordgo.NewShardManager(discordgo.ShardingConfig{
		Token: token,
		// ShardCount 0 = use the recommended count from the API.
		ShardCount: 0,
		ConfigureSession: func(s *discordgo.Session) {
			s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages
		},
	})

	m.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		fmt.Printf("shard %d/%d ready as %s\n", s.ShardID, s.ShardCount, s.State.User.Username)
	})

	m.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}
		fmt.Printf("shard %d: message in %s: %s\n", s.ShardID, m.ChannelID, m.Content)
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := m.OpenContext(ctx); err != nil {
		fmt.Println("error opening shards:", err)
		return
	}
	defer m.Close()

	fmt.Println("all shards connected. Press CTRL-C to exit.")
	<-ctx.Done()
}
