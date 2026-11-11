package discordgo

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

// newReplyTestSession returns a session wired to a test server that records
// interaction callback requests, plus the recorded request slot.
func newReplyTestSession(t *testing.T) (s *Session, gotMethod *string, gotPath *string, gotQuery *url.Values, gotBody *map[string]interface{}, cleanup func()) {
	t.Helper()
	gotMethod = new(string)
	gotPath = new(string)
	gotQuery = new(url.Values)
	gotBody = new(map[string]interface{})
	s, cleanup = newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotMethod = r.Method
		*gotPath = r.URL.Path
		q := r.URL.Query()
		*gotQuery = q
		*gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m9","channel_id":"c1","content":"ok","author":{"id":"u1"},"timestamp":"2026-11-11T00:00:00.000000+00:00"}`))
	}))
	return
}

func testInteraction() *Interaction {
	return &Interaction{ID: "i1", AppID: "app1", Token: "tok", Type: InteractionApplicationCommand}
}

func TestReplyHelpersCallbackBodies(t *testing.T) {
	embed := NewEmbed().Title("Hello").Build()
	modal := NewModal("feedback", "Send feedback").
		AddLabel(NewLabel("Notes").TextInput(NewTextInput("notes")))

	tests := []struct {
		name string
		call func(s *Session, i *Interaction) error
		// wantData checks the decoded "data" object of the callback body.
		wantType  float64
		checkData func(t *testing.T, data map[string]interface{})
	}{
		{
			name:     "Reply",
			call:     func(s *Session, i *Interaction) error { return s.Reply(i, "plain answer") },
			wantType: float64(InteractionResponseChannelMessageWithSource),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				if data["content"] != "plain answer" {
					t.Fatalf("unexpected content: %v", data["content"])
				}
				if _, ok := data["flags"]; ok {
					t.Fatalf("expected no flags, got %v", data["flags"])
				}
			},
		},
		{
			name:     "ReplyEphemeral",
			call:     func(s *Session, i *Interaction) error { return s.ReplyEphemeral(i, "just for you") },
			wantType: float64(InteractionResponseChannelMessageWithSource),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				if data["content"] != "just for you" {
					t.Fatalf("unexpected content: %v", data["content"])
				}
				if data["flags"] != float64(MessageFlagsEphemeral) {
					t.Fatalf("expected ephemeral flag %d, got %v", MessageFlagsEphemeral, data["flags"])
				}
			},
		},
		{
			name:     "ReplyEmbeds",
			call:     func(s *Session, i *Interaction) error { return s.ReplyEmbeds(i, []*MessageEmbed{embed}) },
			wantType: float64(InteractionResponseChannelMessageWithSource),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				embeds, ok := data["embeds"].([]interface{})
				if !ok || len(embeds) != 1 {
					t.Fatalf("expected one embed, got %v", data["embeds"])
				}
				e := embeds[0].(map[string]interface{})
				if e["title"] != "Hello" {
					t.Fatalf("unexpected embed: %v", e)
				}
			},
		},
		{
			name: "ReplyComponents",
			call: func(s *Session, i *Interaction) error {
				row := NewRow().AddButton(NewPrimaryButton("Open", "open-1")).Build()
				return s.ReplyComponents(i, []MessageComponent{&row})
			},
			wantType: float64(InteractionResponseChannelMessageWithSource),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				comps, ok := data["components"].([]interface{})
				if !ok || len(comps) != 1 {
					t.Fatalf("expected one component, got %v", data["components"])
				}
				c := comps[0].(map[string]interface{})
				if c["type"] != float64(ActionsRowComponent) {
					t.Fatalf("expected actions row, got %v", c)
				}
			},
		},
		{
			name:     "ReplyDeferred",
			call:     func(s *Session, i *Interaction) error { return s.ReplyDeferred(i) },
			wantType: float64(InteractionResponseDeferredChannelMessageWithSource),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				if len(data) != 0 {
					t.Fatalf("expected empty data, got %v", data)
				}
			},
		},
		{
			name:     "ReplyDeferredEphemeral",
			call:     func(s *Session, i *Interaction) error { return s.ReplyDeferredEphemeral(i) },
			wantType: float64(InteractionResponseDeferredChannelMessageWithSource),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				if data["flags"] != float64(MessageFlagsEphemeral) {
					t.Fatalf("expected ephemeral flag, got %v", data["flags"])
				}
			},
		},
		{
			name: "ReplyAutocomplete",
			call: func(s *Session, i *Interaction) error {
				i.Type = InteractionApplicationCommandAutocomplete
				return s.ReplyAutocomplete(i, []*ApplicationCommandOptionChoice{
					{Name: "first", Value: "1"},
					{Name: "second", Value: "2"},
				})
			},
			wantType: float64(InteractionApplicationCommandAutocompleteResult),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				choices, ok := data["choices"].([]interface{})
				if !ok || len(choices) != 2 {
					t.Fatalf("expected two choices, got %v", data["choices"])
				}
				c := choices[0].(map[string]interface{})
				if c["name"] != "first" || c["value"] != "1" {
					t.Fatalf("unexpected choice: %v", c)
				}
			},
		},
		{
			name:     "ReplyModal",
			call:     func(s *Session, i *Interaction) error { return s.ReplyModal(i, modal) },
			wantType: float64(InteractionResponseModal),
			checkData: func(t *testing.T, data map[string]interface{}) {
				t.Helper()
				if data["custom_id"] != "feedback" || data["title"] != "Send feedback" {
					t.Fatalf("unexpected modal data: %v", data)
				}
				comps, ok := data["components"].([]interface{})
				if !ok || len(comps) != 1 {
					t.Fatalf("expected one label component, got %v", data["components"])
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, gotMethod, gotPath, _, gotBody, cleanup := newReplyTestSession(t)
			defer cleanup()

			if err := tc.call(s, testInteraction()); err != nil {
				t.Fatal(err)
			}
			if *gotMethod != http.MethodPost {
				t.Fatalf("expected POST, got %s", *gotMethod)
			}
			if *gotPath != "/interactions/i1/tok/callback" {
				t.Fatalf("unexpected path: %s", *gotPath)
			}
			body := *gotBody
			if body["type"] != tc.wantType {
				t.Fatalf("expected response type %v, got %v", tc.wantType, body["type"])
			}
			data, _ := body["data"].(map[string]interface{})
			tc.checkData(t, data)
		})
	}
}

func TestFollowUpHelpers(t *testing.T) {
	tests := []struct {
		name     string
		call     func(s *Session, i *Interaction) (*Message, error)
		wantFlag float64
	}{
		{
			name:     "FollowUp",
			call:     func(s *Session, i *Interaction) (*Message, error) { return s.FollowUp(i, "all done") },
			wantFlag: 0,
		},
		{
			name:     "FollowUpEphemeral",
			call:     func(s *Session, i *Interaction) (*Message, error) { return s.FollowUpEphemeral(i, "psst") },
			wantFlag: float64(MessageFlagsEphemeral),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, gotMethod, gotPath, gotQuery, gotBody, cleanup := newReplyTestSession(t)
			defer cleanup()

			msg, err := tc.call(s, testInteraction())
			if err != nil {
				t.Fatal(err)
			}
			if *gotMethod != http.MethodPost {
				t.Fatalf("expected POST, got %s", *gotMethod)
			}
			if *gotPath != "/webhooks/app1/tok" {
				t.Fatalf("unexpected path: %s", *gotPath)
			}
			if (*gotQuery).Get("wait") != "true" {
				t.Fatalf("expected wait=true, got %s", (*gotQuery).Encode())
			}
			body := *gotBody
			if tc.wantFlag == 0 {
				if _, ok := body["flags"]; ok {
					t.Fatalf("expected no flags, got %v", body["flags"])
				}
			} else if body["flags"] != tc.wantFlag {
				t.Fatalf("expected flags %v, got %v", tc.wantFlag, body["flags"])
			}
			if msg == nil || msg.ID != "m9" {
				t.Fatalf("unexpected followup message: %+v", msg)
			}
		})
	}
}

func TestFollowUpData(t *testing.T) {
	s, gotMethod, gotPath, gotQuery, gotBody, cleanup := newReplyTestSession(t)
	defer cleanup()

	embed := NewEmbed().Title("Later").Build()
	msg, err := s.FollowUpData(testInteraction(), &WebhookParams{Embeds: []*MessageEmbed{embed}})
	if err != nil {
		t.Fatal(err)
	}
	if *gotMethod != http.MethodPost || *gotPath != "/webhooks/app1/tok" {
		t.Fatalf("unexpected request: %s %s", *gotMethod, *gotPath)
	}
	if (*gotQuery).Get("wait") != "true" {
		t.Fatalf("expected wait=true, got %s", (*gotQuery).Encode())
	}
	embeds, ok := (*gotBody)["embeds"].([]interface{})
	if !ok || len(embeds) != 1 {
		t.Fatalf("expected one embed in body, got %v", (*gotBody)["embeds"])
	}
	if msg == nil || msg.ID != "m9" {
		t.Fatalf("unexpected followup message: %+v", msg)
	}
}

func TestReplyAutocompleteRequiresChoices(t *testing.T) {
	s, gotMethod, _, _, _, cleanup := newReplyTestSession(t)
	defer cleanup()

	err := s.ReplyAutocomplete(testInteraction(), nil)
	if !errors.Is(err, ErrNoAutocompleteChoices) {
		t.Fatalf("expected ErrNoAutocompleteChoices, got %v", err)
	}
	err = s.ReplyAutocomplete(testInteraction(), []*ApplicationCommandOptionChoice{})
	if !errors.Is(err, ErrNoAutocompleteChoices) {
		t.Fatalf("expected ErrNoAutocompleteChoices for empty slice, got %v", err)
	}
	if *gotMethod != "" {
		t.Fatalf("no request must be made for empty choices, got %s", *gotMethod)
	}
}
