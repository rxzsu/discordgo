// This example demonstrates bulk banning users with GuildBulkBan.
package main

import (
	"fmt"
	"os"

	"github.com/rxzsu/discordgo"
)

func main() {
	token := os.Getenv("DISCORD_TOKEN")
	guildID := os.Getenv("GUILD_ID")
	if token == "" || guildID == "" {
		fmt.Println("DISCORD_TOKEN and GUILD_ID environment variables are required")
		return
	}

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("error creating session:", err)
		return
	}

	// Ban up to 200 users in a single API call. deleteMessageSeconds
	// (0..604800) removes recent messages of the banned users.
	resp, err := s.GuildBulkBan(guildID,
		[]string{"111111111111111111", "222222222222222222"},
		86400, // delete last 24h of messages
	)
	if err != nil {
		fmt.Println("error bulk banning:", err)
		return
	}

	fmt.Printf("banned:      %v\n", resp.BannedUsers)
	fmt.Printf("not banned:  %v\n", resp.FailedUserIDs)
	_ = s.Close()
}
