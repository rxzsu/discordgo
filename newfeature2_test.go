package discordgo

import (
	"errors"
	"net/http"
	"testing"
)

func TestGuildWelcomeScreen(t *testing.T) {
	var gotMethod string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/welcome-screen" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"description": "Welcome!",
			"welcome_channels": [
				{"channel_id": "c1", "description": "Rules", "emoji_id": "", "emoji_name": "📘"},
				{"channel_id": "c2", "description": "Talk", "emoji_id": "e1", "emoji_name": null}
			]
		}`))
	}))
	defer cleanup()

	screen, err := s.GuildWelcomeScreen("g1")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET request, got %s", gotMethod)
	}
	if screen.Description != "Welcome!" {
		t.Fatalf("unexpected description: %+v", screen)
	}
	if len(screen.WelcomeChannels) != 2 {
		t.Fatalf("expected 2 welcome channels, got %d", len(screen.WelcomeChannels))
	}
	first := screen.WelcomeChannels[0]
	if first.ChannelID != "c1" || first.Description != "Rules" || first.EmojiName != "📘" || first.EmojiID != "" {
		t.Fatalf("unexpected first welcome channel: %+v", first)
	}
	second := screen.WelcomeChannels[1]
	if second.EmojiID != "e1" || second.EmojiName != "" {
		t.Fatalf("custom emoji not parsed: %+v", second)
	}
}

func TestGuildWelcomeScreenEdit(t *testing.T) {
	var (
		gotMethod string
		gotBody   map[string]interface{}
	)
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/welcome-screen" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"description": "Updated",
			"welcome_channels": [
				{"channel_id": "c9", "description": "New", "emoji_id": "", "emoji_name": "🎉"}
			]
		}`))
	}))
	defer cleanup()

	enabled := true
	description := "Updated"
	screen, err := s.GuildWelcomeScreenEdit("g1", &GuildWelcomeScreenParams{
		Enabled:     &enabled,
		Description: &description,
		WelcomeChannels: []WelcomeScreenChannel{
			{ChannelID: "c9", Description: "New", EmojiName: "🎉"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("expected PATCH request, got %s", gotMethod)
	}
	if gotBody["enabled"] != true || gotBody["description"] != "Updated" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}
	channels, ok := gotBody["welcome_channels"].([]interface{})
	if !ok || len(channels) != 1 {
		t.Fatalf("expected one welcome channel in request body, got %v", gotBody)
	}
	channel, ok := channels[0].(map[string]interface{})
	if !ok || channel["channel_id"] != "c9" || channel["emoji_name"] != "🎉" {
		t.Fatalf("unexpected welcome channel in request body: %v", channels)
	}
	if screen.Description != "Updated" || len(screen.WelcomeChannels) != 1 {
		t.Fatalf("unexpected response payload: %+v", screen)
	}
}

func TestGuildWelcomeScreenEditNilParams(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be made for nil params")
	}))
	defer cleanup()

	if _, err := s.GuildWelcomeScreenEdit("g1", nil); !errors.Is(err, ErrNilWelcomeScreenParams) {
		t.Fatalf("expected ErrNilWelcomeScreenParams, got %v", err)
	}
}

func TestGuildWelcomeScreenEditNilFieldsOmitted(t *testing.T) {
	var gotBody map[string]interface{}
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeFeatureTestBody(t, r)
		_, _ = w.Write([]byte(`{"description": "", "welcome_channels": []}`))
	}))
	defer cleanup()

	if _, err := s.GuildWelcomeScreenEdit("g1", &GuildWelcomeScreenParams{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["enabled"]; ok {
		t.Fatalf("expected enabled to be omitted, got %s", mustJSON(t, gotBody))
	}
	if _, ok := gotBody["description"]; ok {
		t.Fatalf("expected description to be omitted, got %s", mustJSON(t, gotBody))
	}
	if _, ok := gotBody["welcome_channels"]; ok {
		t.Fatalf("expected welcome_channels to be omitted, got %s", mustJSON(t, gotBody))
	}
}

func TestGuildVanityURL(t *testing.T) {
	var gotMethod string
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/vanity-url" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code": "vanity", "uses": 1234}`))
	}))
	defer cleanup()

	vanity, err := s.GuildVanityURL("g1")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected GET request, got %s", gotMethod)
	}
	if vanity.Code != "vanity" || vanity.Uses != 1234 {
		t.Fatalf("unexpected vanity url: %+v", vanity)
	}
}

func TestGuildVanityURLEdit(t *testing.T) {
	var (
		gotMethod string
		gotBody   map[string]interface{}
	)
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/vanity-url" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code": "newcode", "uses": 0}`))
	}))
	defer cleanup()

	vanity, err := s.GuildVanityURLEdit("g1", "newcode")
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPatch {
		t.Fatalf("expected PATCH request, got %s", gotMethod)
	}
	if gotBody["code"] != "newcode" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}
	if vanity.Code != "newcode" || vanity.Uses != 0 {
		t.Fatalf("unexpected response payload: %+v", vanity)
	}
}

func TestGuildVanityURLEditEmptyCode(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be made for an empty code")
	}))
	defer cleanup()

	if _, err := s.GuildVanityURLEdit("g1", ""); !errors.Is(err, ErrEmptyGuildVanityCode) {
		t.Fatalf("expected ErrEmptyGuildVanityCode, got %v", err)
	}
}

func TestGuildOnboardingEditPartial(t *testing.T) {
	var (
		gotMethod string
		gotQuery  string
		gotBody   map[string]interface{}
	)
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/guilds/g1/onboarding" {
			http.NotFound(w, r)
			return
		}
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"guild_id": "g1",
			"prompts": [
				{"id": "p1", "type": 0, "title": "Pick roles", "single_select": true, "required": true, "in_onboarding": true,
				 "options": [{"id": "o1", "title": "Role A", "description": "", "channel_ids": [], "role_ids": ["r1"]}]}
			],
			"enabled": true
		}`))
	}))
	defer cleanup()

	enabled := true
	onboarding, err := s.GuildOnboardingEditPartial("g1", &GuildOnboardingPartialEditParams{
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT request, got %s", gotMethod)
	}
	if gotQuery != "partial=true" {
		t.Fatalf("expected partial=true query, got %q", gotQuery)
	}
	if _, ok := gotBody["prompts"]; ok {
		t.Fatalf("expected prompts to be omitted from a partial update, got %s", mustJSON(t, gotBody))
	}
	if _, ok := gotBody["default_channel_ids"]; ok {
		t.Fatalf("expected default_channel_ids to be omitted from a partial update, got %s", mustJSON(t, gotBody))
	}
	if gotBody["enabled"] != true {
		t.Fatalf("expected enabled=true in request body, got %v", gotBody["enabled"])
	}
	if onboarding.GuildID != "g1" || onboarding.Enabled == nil || !*onboarding.Enabled {
		t.Fatalf("unexpected response payload: %+v", onboarding)
	}
}

func TestGuildOnboardingEditPartialPrompts(t *testing.T) {
	var gotBody map[string]interface{}
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeFeatureTestBody(t, r)
		_, _ = w.Write([]byte(`{"guild_id": "g1", "prompts": [], "enabled": true}`))
	}))
	defer cleanup()

	prompts := []*GuildOnboardingPrompt{{
		ID:           "p1",
		Title:        "Pick roles",
		SingleSelect: true,
		Required:     true,
		InOnboarding: true,
		Options: []GuildOnboardingPromptOption{{
			ID:      "o1",
			Title:   "Role A",
			RoleIDs: []string{"r1"},
		}},
	}}
	if _, err := s.GuildOnboardingEditPartial("g1", &GuildOnboardingPartialEditParams{
		Prompts: prompts,
	}); err != nil {
		t.Fatal(err)
	}

	raw, ok := gotBody["prompts"].([]interface{})
	if !ok || len(raw) != 1 {
		t.Fatalf("expected one prompt in request body, got %v", gotBody)
	}
	if _, ok := gotBody["enabled"]; ok {
		t.Fatalf("expected enabled to be omitted, got %s", mustJSON(t, gotBody))
	}
	if _, ok := gotBody["mode"]; ok {
		t.Fatalf("expected mode to be omitted, got %s", mustJSON(t, gotBody))
	}
}

func TestGuildOnboardingEditPartialEmptyPromptsSent(t *testing.T) {
	var gotBody map[string]interface{}
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = decodeFeatureTestBody(t, r)
		_, _ = w.Write([]byte(`{"guild_id": "g1", "prompts": [], "enabled": true}`))
	}))
	defer cleanup()

	if _, err := s.GuildOnboardingEditPartial("g1", &GuildOnboardingPartialEditParams{
		Prompts: []*GuildOnboardingPrompt{},
	}); err != nil {
		t.Fatal(err)
	}

	raw, ok := gotBody["prompts"].([]interface{})
	if !ok || len(raw) != 0 {
		t.Fatalf("expected an explicit empty prompts list in request body, got %s", mustJSON(t, gotBody))
	}
}

func TestGuildOnboardingEditPartialNilAndEmptyParams(t *testing.T) {
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request should be made for nil or empty params")
	}))
	defer cleanup()

	if _, err := s.GuildOnboardingEditPartial("g1", nil); !errors.Is(err, ErrNilOnboardingPartialEditParams) {
		t.Fatalf("expected ErrNilOnboardingPartialEditParams, got %v", err)
	}
	if _, err := s.GuildOnboardingEditPartial("g1", &GuildOnboardingPartialEditParams{}); !errors.Is(err, ErrEmptyOnboardingPartialEditParams) {
		t.Fatalf("expected ErrEmptyOnboardingPartialEditParams, got %v", err)
	}
}

func TestGuildFeatureEndpoints(t *testing.T) {
	cases := map[string]string{
		EndpointGuildWelcomeScreen("g1"): "https://discord.com/api/v9/guilds/g1/welcome-screen",
		EndpointGuildVanityURL("g1"):     "https://discord.com/api/v9/guilds/g1/vanity-url",
		EndpointGuildOnboarding("g1"):    "https://discord.com/api/v9/guilds/g1/onboarding",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("endpoint %q, want %q", got, want)
		}
	}
}
