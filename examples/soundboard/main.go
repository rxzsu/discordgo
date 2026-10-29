// This example demonstrates the soundboard API: listing guild sounds,
// uploading one, and playing it in a voice channel.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/rxzsu/discordgo"
)

func main() {
	token := os.Getenv("DISCORD_TOKEN")
	guildID := os.Getenv("GUILD_ID")
	channelID := os.Getenv("VOICE_CHANNEL_ID")
	if token == "" || guildID == "" || channelID == "" {
		fmt.Println("DISCORD_TOKEN, GUILD_ID and VOICE_CHANNEL_ID environment variables are required")
		return
	}

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("error creating session:", err)
		return
	}

	// List existing sounds.
	sounds, err := s.GuildSoundboardSounds(guildID)
	if err != nil {
		fmt.Println("error listing soundboard sounds:", err)
		return
	}
	for _, snd := range sounds {
		fmt.Printf("- %s (%s, volume %.2f)\n", snd.Name, snd.ID, snd.Volume)
	}

	// Upload a new sound (MP3/OGG, max 1 MB, max ~5.2 seconds).
	soundFile, err := os.Open("mysound.ogg")
	if err != nil {
		fmt.Println("cannot open mysound.ogg:", err)
		return
	}
	defer soundFile.Close()

	created, err := s.GuildSoundboardSoundCreate(guildID,
		&discordgo.SoundboardSoundParams{Name: "airhorn", Volume: 0.8, EmojiName: "📢"},
		&discordgo.File{Name: "mysound.ogg", Reader: soundFile, ContentType: "audio/ogg"},
	)
	if err != nil {
		fmt.Println("error creating soundboard sound:", err)
		return
	}
	fmt.Println("uploaded sound:", created.ID)

	// Play it in a voice channel (the bot must be connected to it).
	if err := s.ChannelSoundboardSoundSend(channelID, created.ID, guildID); err != nil {
		fmt.Println("error sending soundboard sound:", err)
		if strings.Contains(err.Error(), "50001") {
			fmt.Println("(missing permissions: needs SEND + USE_SOUNDBOARD)")
		}
		return
	}
	fmt.Println("sound sent to", channelID)
	_ = s.Close()
}
