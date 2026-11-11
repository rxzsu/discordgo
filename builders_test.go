package discordgo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// marshalForWire marshals v exactly like the library does on the wire (the
// replaceable Marshal variable, encoding/json in the default build).
func marshalForWire(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(b)
}

// prettyDiff renders two JSON strings indented so test failures are
// readable.
func prettyDiff(t *testing.T, want, got string) {
	t.Helper()
	var wb, gb bytes.Buffer
	if json.Indent(&wb, []byte(want), "", " ") == nil && json.Indent(&gb, []byte(got), "", " ") == nil {
		t.Fatalf("JSON mismatch:\nwant: %s\ngot:  %s", wb.String(), gb.String())
	}
	t.Fatalf("JSON mismatch:\nwant: %s\ngot:  %s", want, got)
}

func TestEmbedBuilderJSON(t *testing.T) {
	ts := time.Date(2026, 11, 11, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name  string
		build func() *MessageEmbed
		want  string
	}{
		{
			name:  "empty",
			build: func() *MessageEmbed { return NewEmbed().Build() },
			want:  `{}`,
		},
		{
			name: "full",
			build: func() *MessageEmbed {
				return NewEmbed().
					Title("Release notes").
					Description("Everything new").
					URL("https://example.com/readme").
					Color(0x5865F2).
					Timestamp(ts).
					Author("rxzsu", "https://example.com/a.png", "https://example.com/user").
					Footer("fork", "https://example.com/f.png").
					Image("https://example.com/i.png").
					Thumbnail("https://example.com/t.png").
					Field("Typed handlers", "compile-time safe", true).
					Field("Builders", "fluent DSL", false).
					Build()
			},
			want: `{"url":"https://example.com/readme","title":"Release notes","description":"Everything new","timestamp":"2026-11-11T10:30:00Z","color":5793266,` +
				`"footer":{"text":"fork","icon_url":"https://example.com/f.png"},` +
				`"image":{"url":"https://example.com/i.png"},` +
				`"thumbnail":{"url":"https://example.com/t.png"},` +
				`"author":{"url":"https://example.com/user","name":"rxzsu","icon_url":"https://example.com/a.png"},` +
				`"fields":[{"name":"Typed handlers","value":"compile-time safe","inline":true},{"name":"Builders","value":"fluent DSL"}]}`,
		},
		{
			name:  "zero timestamp is omitted",
			build: func() *MessageEmbed { return NewEmbed().Title("t").Timestamp(time.Time{}).Build() },
			want:  `{"title":"t"}`,
		},
		{
			name: "fields via Fields",
			build: func() *MessageEmbed {
				return NewEmbed().Fields(
					&MessageEmbedField{Name: "n1", Value: "v1", Inline: true},
					&MessageEmbedField{Name: "n2", Value: "v2"},
				).Build()
			},
			want: `{"fields":[{"name":"n1","value":"v1","inline":true},{"name":"n2","value":"v2"}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalForWire(t, tc.build())
			if got != tc.want {
				prettyDiff(t, tc.want, got)
			}
		})
	}
}

// TestEmbedBuilderBuildIsCopy ensures later builder edits do not mutate a
// previously built embed.
func TestEmbedBuilderBuildIsCopy(t *testing.T) {
	b := NewEmbed().Title("first")
	e1 := b.Build()
	b.Title("second")
	e2 := b.Build()
	if e1.Title != "first" || e2.Title != "second" {
		t.Fatalf("Build() did not snapshot builder state: %q / %q", e1.Title, e2.Title)
	}
}

func TestButtonRowBuilderJSON(t *testing.T) {
	tests := []struct {
		name  string
		build func() ActionsRow
		want  string
	}{
		{
			name: "primary and danger buttons",
			build: func() ActionsRow {
				return NewRow().AddButton(NewPrimaryButton("Click", "btn-1")).AddButton(NewDangerButton("Deny", "btn-2")).Build()
			},
			want: `{"components":[{"label":"Click","style":1,"disabled":false,"custom_id":"btn-1","type":2},{"label":"Deny","style":4,"disabled":false,"custom_id":"btn-2","type":2}],"type":1}`,
		},
		{
			name:  "link button with url",
			build: func() ActionsRow { return NewRow().AddButton(NewLinkButton("Docs", "https://example.com")).Build() },
			want:  `{"components":[{"label":"Docs","style":5,"disabled":false,"url":"https://example.com","type":2}],"type":1}`,
		},
		{
			name: "disabled button with unicode emoji",
			build: func() ActionsRow {
				return NewRow().AddButton(NewButton().Label("Meh").Style(SecondaryButton).CustomID("meh").EmojiName("🎉").Disabled()).Build()
			},
			want: `{"components":[{"label":"Meh","style":2,"disabled":true,"emoji":{"name":"🎉"},"custom_id":"meh","type":2}],"type":1}`,
		},
		{
			name: "raw button",
			build: func() ActionsRow {
				return NewRow().AddButtonRaw(Button{Label: "Raw", Style: SuccessButton, CustomID: "raw"}).Build()
			},
			want: `{"components":[{"label":"Raw","style":3,"disabled":false,"custom_id":"raw","type":2}],"type":1}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalForWire(t, tc.build())
			if got != tc.want {
				prettyDiff(t, tc.want, got)
			}
		})
	}
}

func TestSelectMenuBuilderJSON(t *testing.T) {
	tests := []struct {
		name  string
		build func() SelectMenu
		want  string
	}{
		{
			name: "string select with options",
			build: func() SelectMenu {
				return NewSelectMenu("pick").
					Placeholder("Choose").
					Option("A", "a").
					OptionDesc("B", "b", "the b option").
					MinValues(1).
					MaxValues(2).
					Build()
			},
			want: `{"custom_id":"pick","placeholder":"Choose","min_values":1,"max_values":2,` +
				`"options":[{"label":"A","value":"a","description":"","default":false},{"label":"B","value":"b","description":"the b option","default":false}],` +
				`"disabled":false,"type":3}`,
		},
		{
			name:  "user select is auto-populated",
			build: func() SelectMenu { return NewUserSelectMenu("user-pick").Placeholder("Pick a user").Build() },
			want:  `{"custom_id":"user-pick","placeholder":"Pick a user","disabled":false,"type":5}`,
		},
		{
			name: "channel select with channel types and defaults",
			build: func() SelectMenu {
				return NewChannelSelectMenu("chan-pick").
					ChannelTypes(ChannelTypeGuildText, ChannelTypeGuildVoice).
					DefaultValues(
						SelectMenuDefaultValue{ID: "c1", Type: SelectMenuDefaultValueChannel},
					).
					Build()
			},
			want: `{"custom_id":"chan-pick","placeholder":"",` +
				`"default_values":[{"id":"c1","type":"channel"}],"disabled":false,"channel_types":[0,2],"type":8}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalForWire(t, tc.build())
			if got != tc.want {
				prettyDiff(t, tc.want, got)
			}
		})
	}
}

func TestModalBuilderJSON(t *testing.T) {
	resp := NewModal("feedback", "Send feedback").
		AddLabel(NewLabel("Your feedback").
			Description("What can we improve?").
			TextInput(NewTextInput("feedback_text").
				Style(TextInputParagraph).
				Placeholder("Type here...").
				Required(true).
				MinLength(1).
				MaxLength(200))).
		Build()

	want := `{"type":9,"data":{"tts":false,"content":"","components":[` +
		`{"label":"Your feedback","description":"What can we improve?","component":{"custom_id":"feedback_text","style":2,"placeholder":"Type here...","required":true,"min_length":1,"max_length":200,"type":4},"type":18}` +
		`],"embeds":null,"custom_id":"feedback","title":"Send feedback"}}`

	got := marshalForWire(t, resp)
	if got != want {
		prettyDiff(t, want, got)
	}

	// The response must be ready for the regular respond path unchanged.
	if resp.Type != InteractionResponseModal {
		t.Fatalf("expected modal response type 9, got %d", resp.Type)
	}
	if resp.Data == nil || resp.Data.CustomID != "feedback" {
		t.Fatalf("unexpected modal data: %+v", resp.Data)
	}
}

func TestMessageBuilderJSON(t *testing.T) {
	embed := NewEmbed().Title("Title").Build()

	built := NewMessage().
		Content("hello").
		AddEmbed(embed).
		AddRow(NewRow().AddButton(NewPrimaryButton("Open", "open-1"))).
		TTS(true).
		StickerIDs("s1", "s2").
		Build()

	// The built payload must be exactly what the hand-written struct
	// literal produces.
	row := NewRow().AddButton(NewPrimaryButton("Open", "open-1")).Build()
	want := &MessageSend{
		Content:    "hello",
		Embeds:     []*MessageEmbed{embed},
		Components: []MessageComponent{&row},
		TTS:        true,
		StickerIDs: []string{"s1", "s2"},
	}

	gotJSON := marshalForWire(t, built)
	wantJSON := marshalForWire(t, want)
	if gotJSON != wantJSON {
		prettyDiff(t, wantJSON, gotJSON)
	}
	if !strings.Contains(gotJSON, `"content":"hello"`) || !strings.Contains(gotJSON, `"custom_id":"open-1"`) {
		t.Fatalf("unexpected wire JSON: %s", gotJSON)
	}
}

func TestMessageBuilderSend(t *testing.T) {
	var (
		gotPath string
		gotBody map[string]interface{}
	)
	s, cleanup := newFeatureTestSession(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = decodeFeatureTestBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m1","channel_id":"c1","content":"hello","author":{"id":"u1"},"timestamp":"2026-11-11T00:00:00.000000+00:00"}`))
	}))
	defer cleanup()

	msg, err := NewMessage().
		Content("hello").
		AddRow(NewRow().AddButton(NewPrimaryButton("Open", "open-1"))).
		Send(s, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/channels/c1/messages" {
		t.Fatalf("unexpected request path: %s", gotPath)
	}
	if msg == nil || msg.ID != "m1" {
		t.Fatalf("unexpected response message: %+v", msg)
	}
	if gotBody["content"] != "hello" {
		t.Fatalf("unexpected request body: %v", gotBody)
	}
	comps, ok := gotBody["components"].([]interface{})
	if !ok || len(comps) != 1 {
		t.Fatalf("expected one component row in request body, got %v", gotBody["components"])
	}
}

// TestMessageBuilderBuildIsCopy ensures later builder edits do not mutate a
// previously built payload.
func TestMessageBuilderBuildIsCopy(t *testing.T) {
	b := NewMessage().Content("first")
	m1 := b.Build()
	b.Content("second")
	m2 := b.Build()
	if m1.Content != "first" || m2.Content != "second" {
		t.Fatalf("Build() did not snapshot builder state: %q / %q", m1.Content, m2.Content)
	}
}
