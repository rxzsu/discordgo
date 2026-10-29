// This example demonstrates joining a voice channel, sending opus audio
// from a DCA file, and reporting voice heartbeat latency.
// DCA files (raw opus frames) can be produced with the dca tool:
// https://github.com/bwmarrin/dca
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rxzsu/discordgo"
)

func loadDCA(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var frames [][]byte
	var lenBuf int16
	for {
		// DCA format: int16 frame length followed by opus data.
		if err := binary.Read(file, binary.LittleEndian, &lenBuf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		frame := make([]byte, lenBuf)
		if _, err := io.ReadFull(file, frame); err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

func main() {
	token := os.Getenv("DISCORD_TOKEN")
	guildID := os.Getenv("GUILD_ID")
	channelID := os.Getenv("VOICE_CHANNEL_ID")
	dcaFile := os.Getenv("DCA_FILE")
	if token == "" || guildID == "" || channelID == "" || dcaFile == "" {
		fmt.Println("DISCORD_TOKEN, GUILD_ID, VOICE_CHANNEL_ID and DCA_FILE environment variables are required")
		return
	}

	s, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("error creating session:", err)
		return
	}

	if err := s.Open(); err != nil {
		fmt.Println("error opening gateway:", err)
		return
	}
	defer s.Close()

	vc, err := s.ChannelVoiceJoin(guildID, channelID, false, true)
	if err != nil {
		fmt.Println("error joining voice channel:", err)
		return
	}
	defer vc.Disconnect()

	// Wait for the UDP connection to be ready.
	for i := 0; i < 20 && !vc.Ready; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !vc.Ready {
		fmt.Println("voice connection did not become ready")
		return
	}

	frames, err := loadDCA(dcaFile)
	if err != nil {
		fmt.Println("error loading DCA file:", err)
		return
	}
	fmt.Printf("loaded %d opus frames\n", len(frames))

	if err := vc.Speaking(true); err != nil {
		fmt.Println("error setting speaking:", err)
		return
	}
	for _, frame := range frames {
		vc.OpusSend <- frame
		time.Sleep(20 * time.Millisecond) // 960 samples @ 48kHz
	}
	if err := vc.Speaking(false); err != nil {
		fmt.Println("error unsetting speaking:", err)
	}

	// Voice websocket latency: new in this fork.
	fmt.Println("voice heartbeat latency:", vc.VoiceHeartbeatLatency())
	if strings.HasPrefix(vc.VoiceHeartbeatLatency().String(), "0s") && vc.LastVoiceHeartbeatAck.IsZero() {
		fmt.Println("(no heartbeat exchange observed yet)")
	}

	fmt.Println("done. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}
