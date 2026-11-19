package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rxzsu/discordgo"
)

type surveyTransport func(*http.Request) (*http.Response, error)

func (f surveyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestComponentsV2Survey(t *testing.T) {
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		method, path, required   string
		isCallback, componentsV2 bool
	}{
		{"POST", "/channels/123/messages", openSurvey, false, true},
		{"POST", "/interactions/7/button-token/callback", submitSurvey, true, false},
		{"POST", "/interactions/8/modal-token/callback", `"type":5`, true, false},
		{"PATCH", "/webhooks/42/modal-token/messages/@original", "**Gateway**", false, true},
		{"POST", "/interactions/9/direct-token/callback", "Direct reply", true, true},
		{"PATCH", "/channels/123/messages/456", "Updated survey", false, true},
	}
	next := 0
	s.Client = &http.Client{Transport: surveyTransport(func(r *http.Request) (*http.Response, error) {
		if next >= len(steps) {
			return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		step := steps[next]
		next++
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if r.Method != step.method || !strings.HasSuffix(r.URL.Path, step.path) || !strings.Contains(string(body), step.required) {
			t.Errorf("step %d: unexpected request %s %s %s", next, r.Method, r.URL.Path, body)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
		if step.isCallback {
			var data map[string]json.RawMessage
			if err := json.Unmarshal(payload["data"], &data); err != nil {
				return nil, err
			}
			payload = data
		}
		if step.componentsV2 {
			if string(payload["flags"]) != "32768" || len(payload["components"]) == 0 {
				t.Errorf("step %d: missing Components V2 payload: %s", next, body)
			}
		}
		if step.componentsV2 || step.isCallback {
			for _, key := range []string{"content", "embeds", "tts", "poll", "sticker_ids"} {
				if _, ok := payload[key]; ok {
					t.Errorf("step %d: unexpected %q in %s", next, key, body)
				}
			}
		}
		if step.required == `"type":5` && string(payload["flags"]) != "64" {
			t.Errorf("expected ephemeral defer, got %s", body)
		}
		reply := `{"id":"456","flags":32768,"components":[{"type":10,"content":"Example"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(reply))}, nil
	})}
	if _, err := sendSurvey(s, "123"); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{
		`{"id":"7","type":3,"token":"button-token","data":{"component_type":2,"custom_id":"components-v2:open"}}`,
		`{"id":"8","application_id":"42","type":5,"token":"modal-token","data":{"custom_id":"components-v2:submit","components":[{"type":18,"component":{"type":21,"custom_id":"topic","value":"gateway"}}]}}`,
	} {
		var interaction discordgo.Interaction
		if err := json.Unmarshal([]byte(event), &interaction); err != nil {
			t.Fatal(err)
		}
		if err := handleSurvey(s, &interaction); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InteractionRespond(&discordgo.Interaction{ID: "9", Token: "direct-token"}, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsIsComponentsV2,
			Components: []discordgo.MessageComponent{discordgo.NewTextDisplay("Direct reply")},
		},
	}); err != nil {
		t.Fatal(err)
	}
	edit := discordgo.NewMessageEdit("123", "456")
	edit.Flags = discordgo.MessageFlagsIsComponentsV2
	edit.Components = &[]discordgo.MessageComponent{discordgo.NewTextDisplay("Updated survey")}
	if _, err := s.ChannelMessageEditComplex(edit); err != nil {
		t.Fatal(err)
	}
	if next != len(steps) {
		t.Fatalf("expected %d requests, got %d", len(steps), next)
	}
}
