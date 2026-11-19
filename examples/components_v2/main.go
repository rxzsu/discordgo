package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/rxzsu/discordgo"
)

const (
	openSurvey   = "components-v2:open"
	submitSurvey = "components-v2:submit"
	topicField   = "topic"
)

func main() {
	channel := flag.String("channel", "", "Channel ID for the survey")
	flag.Parse()
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" || *channel == "" {
		log.Fatal("Set DISCORD_BOT_TOKEN and supply -channel")
	}
	s, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal(err)
	}
	s.Identify.Intents = discordgo.IntentsGuilds
	discordgo.AddHandlerTyped(s, func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if err := handleSurvey(s, i.Interaction); err != nil {
			log.Printf("survey interaction failed: %v", err)
		}
	})
	if err := s.Open(); err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	panel, err := sendSurvey(s, *channel)
	if err != nil {
		log.Printf("send survey: %v", err)
		return
	}
	log.Printf("Survey posted as message %s. Press Ctrl+C to stop.", panel.ID)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	defer signal.Stop(stop)
	<-stop
}

func sendSurvey(s *discordgo.Session, channel string) (*discordgo.Message, error) {
	panel := discordgo.NewContainer().
		AddComponent(discordgo.NewTextDisplay("## What should we cover next?")).
		AddComponent(discordgo.NewRow().
			AddButton(discordgo.NewPrimaryButton("Choose a topic", openSurvey)).
			Build()).
		Build()
	return discordgo.NewMessage().ComponentsV2().AddComponent(panel).Send(s, channel)
}

func handleSurvey(s *discordgo.Session, i *discordgo.Interaction) error {
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		if i.MessageComponentData().CustomID != openSurvey {
			return nil
		}
		topics := discordgo.NewRadioGroup(topicField).Required(true).
			AddOption(discordgo.ModalChoice{Label: "Gateway", Value: "gateway"}).
			AddOption(discordgo.ModalChoice{Label: "Components", Value: "components"})
		modal := discordgo.NewModal(submitSurvey, "Choose a topic").
			AddLabel(discordgo.NewLabel("Topic").RadioGroup(topics))
		return s.ReplyModal(i, modal)
	case discordgo.InteractionModalSubmit:
		data := i.ModalSubmitData()
		if data.CustomID != submitSurvey {
			return nil
		}
		topic := selectedTopic(data)
		if topic == "" {
			return s.ReplyEphemeral(i, "No valid topic was selected.")
		}
		if err := s.ReplyDeferredEphemeral(i); err != nil {
			return err
		}
		components := []discordgo.MessageComponent{discordgo.NewTextDisplay("Thanks! You selected **" + topic + "**.")}
		_, err := s.InteractionResponseEdit(i, &discordgo.WebhookEdit{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: &components,
		})
		return err
	}
	return nil
}

func selectedTopic(data discordgo.ModalSubmitInteractionData) string {
	for _, component := range data.Components {
		label, ok := component.(*discordgo.Label)
		if !ok {
			continue
		}
		group, ok := label.Component.(*discordgo.RadioGroup)
		if !ok || group.CustomID != topicField || group.Value == nil {
			continue
		}
		switch *group.Value {
		case "gateway":
			return "Gateway"
		case "components":
			return "Components"
		}
	}
	return ""
}
