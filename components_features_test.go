package discordgo

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestModalChoiceComponents(t *testing.T) {
	modal := NewModal("survey", "Survey").
		AddLabel(NewLabel("Plan").RadioGroup(NewRadioGroup("plan").Required(false).
			AddOption(ModalChoice{Value: "free", Label: "Free", Default: true}).
			AddOption(ModalChoice{Value: "paid", Label: "Paid"}))).
		AddLabel(NewLabel("Days").CheckboxGroup(NewCheckboxGroup("days").Required(false).MinValues(0).
			AddOption(ModalChoice{Value: "mon", Label: "Monday"}))).
		AddLabel(NewLabel("Subscribe").Checkbox(NewCheckbox("subscribe").Default(true))).Build()
	wire := marshalForWire(t, modal)
	var output struct {
		Data ModalSubmitInteractionData `json:"data"`
	}
	if err := json.Unmarshal([]byte(wire), &output); err != nil {
		t.Fatal(err)
	}
	radio := output.Data.Components[0].(*Label).Component.(*RadioGroup)
	group := output.Data.Components[1].(*Label).Component.(*CheckboxGroup)
	checkbox := output.Data.Components[2].(*Label).Component.(*Checkbox)
	if radio.Required == nil || *radio.Required || len(radio.Options) != 2 || !radio.Options[0].Default ||
		group.MinValues == nil || *group.MinValues != 0 || len(group.Options) != 1 ||
		!checkbox.Default || checkbox.Value != nil {
		t.Fatalf("unexpected modal request: %s", wire)
	}

	const submitted = `{"type":5,"data":{"custom_id":"survey","components":[
		{"type":18,"component":{"type":21,"id":2,"custom_id":"plan","value":null}},
		{"type":18,"component":{"type":22,"id":4,"custom_id":"days","values":["mon"]}},
		{"type":18,"component":{"type":23,"id":6,"custom_id":"subscribe","value":false}},
		{"type":18,"component":{"type":21,"id":8,"custom_id":"second","value":"paid"}}
	]}}`
	var interaction Interaction
	if err := json.Unmarshal([]byte(submitted), &interaction); err != nil {
		t.Fatal(err)
	}
	components := interaction.ModalSubmitData().Components
	radio = components[0].(*Label).Component.(*RadioGroup)
	group = components[1].(*Label).Component.(*CheckboxGroup)
	checkbox = components[2].(*Label).Component.(*Checkbox)
	selected := components[3].(*Label).Component.(*RadioGroup)
	if radio.Value != nil || !slices.Equal(group.Values, []string{"mon"}) || checkbox.Value == nil ||
		*checkbox.Value || selected.Value == nil || *selected.Value != "paid" {
		t.Fatalf("unexpected modal values: %+v %+v %+v %+v", radio, group, checkbox, selected)
	}
}

func TestUnknownComponentPreservesJSON(t *testing.T) {
	const component = `{"type":50,"future":{"content":"next"}}`
	var message Message
	if err := json.Unmarshal([]byte(`{"components":[`+component+`,{"type":10,"content":"current"}]}`), &message); err != nil {
		t.Fatal(err)
	}
	if unknown, ok := message.Components[0].(*UnknownComponent); !ok || unknown.Type() != 50 ||
		marshalForWire(t, unknown) != component {
		t.Fatalf("unexpected unknown component: %#v", message.Components[0])
	}
	if _, ok := message.Components[1].(*TextDisplay); !ok {
		t.Fatalf("unexpected known component: %#v", message.Components[1])
	}
	if _, err := MessageComponentFromJSON([]byte(`{"missing":"type"}`)); err == nil {
		t.Fatal("accepted component without type")
	}
}

func TestMessageBuilderComponentsV2Wire(t *testing.T) {
	section := NewSection(NewLinkButton("Guide", "https://example.com").Build()).AddText("Read the guide").Build()
	gallery := NewMediaGallery().AddItem(MediaGalleryItem{Media: UnfurledMediaItem{URL: "https://example.com/image.png"}}).Build()
	container := NewContainer().AccentColor(0).AddComponent(section).AddComponent(gallery).Build()
	builder := NewMessage().ComponentsV2().Flags(MessageFlagsSuppressNotifications).AddComponent(container)
	data := builder.Build()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(marshalForWire(t, data)), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"content", "embeds", "sticker_ids", "poll"} {
		if _, ok := payload[key]; ok {
			t.Errorf("unexpected legacy field %s in Components V2 payload", key)
		}
	}
	if data.Flags != MessageFlagsIsComponentsV2|MessageFlagsSuppressNotifications || !json.Valid(payload["components"]) {
		t.Fatalf("unexpected Components V2 payload: %v", payload)
	}
	if _, err := Marshal(builder.Content("legacy content").Build()); err == nil {
		t.Fatal("expected validation error for legacy content")
	}
}

func TestMessageNonceWire(t *testing.T) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(marshalForWire(t, NewMessage().Content("hello").Nonce("once", true).Build())), &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["nonce"]) != `"once"` || string(payload["enforce_nonce"]) != "true" {
		t.Fatalf("unexpected nonce fields: %v", payload)
	}
	const nonce = "9223372036854775807"
	var message Message
	if err := json.Unmarshal([]byte(`{"nonce":`+nonce+`}`), &message); err != nil || string(message.Nonce) != nonce {
		t.Fatalf("nonce lost precision: %s (%v)", message.Nonce, err)
	}
}

func TestMessageNoncePersistsAcrossRESTRetry(t *testing.T) {
	session, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	client := &http.Client{Transport: featureTestTransport(func(req *http.Request) (*http.Response, error) {
		data, readErr := io.ReadAll(req.Body)
		if readErr != nil {
			return nil, readErr
		}
		bodies = append(bodies, string(data))
		status, body := http.StatusServiceUnavailable, `{}`
		if len(bodies) == 2 {
			status, body = http.StatusOK, `{"id":"123","nonce":"once"}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	message, err := NewMessage().Content("hello").Nonce("once", true).Send(session, "321", WithClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || string(message.Nonce) != `"once"` {
		t.Fatalf("unexpected retry payloads %q and response nonce %s", bodies, message.Nonce)
	}
}
