package discordgo

import (
	"strings"
	"testing"
)

// messageCreatePayload is the shape of a real MESSAGE_CREATE gateway
// payload, including nested author/member structs that exercise the typed
// struct decode.
var messageCreatePayload = []byte(`{"t":"MESSAGE_CREATE","s":1,"op":0,"d":{"id":"1","channel_id":"c","guild_id":"g","author":{"id":"u","username":"n","discriminator":"1234","avatar":"abc"},"content":"hello world","timestamp":"2023-01-02T03:04:05.000000+00:00","edited_timestamp":null,"tts":false,"mention_everyone":false,"mentions":[],"mention_roles":[],"attachments":[],"embeds":[],"type":0,"member":{"roles":["1","2"],"joined_at":"2022-01-01T00:00:00.000000+00:00","deaf":false,"mute":false},"flags":0,"components":[]}}`)

// BenchmarkJSONHooksUnmarshalMessage measures the Marshal/Unmarshal hooks
// (encoding/json by default) on a realistic gateway payload. Run with
// -tags goccy / -tags sonic to compare the alternative JSON back ends.
func BenchmarkJSONHooksUnmarshalMessage(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(messageCreatePayload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var m Message
		if err := Unmarshal(messageCreatePayload, &m); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJSONHooksMarshalMessage measures marshalling the same message
// back out, the shape of REST request bodies (e.g. message edit).
func BenchmarkJSONHooksMarshalMessage(b *testing.B) {
	var m Message
	if err := Unmarshal(messageCreatePayload, &m); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(messageCreatePayload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Marshal(&m); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkJSONHooksUnmarshalEnvelope measures the envelope-only decode that
// parseGatewayEnvelope replaces; kept for a like-for-like back-end
// comparison at the smallest gateway frame size.
func BenchmarkJSONHooksUnmarshalEnvelope(b *testing.B) {
	payload := []byte(`{"t":"MESSAGE_CREATE","s":1,"op":0,"d":{"id":"1","content":"` + strings.Repeat("x", 128) + `"}}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var e Event
		if err := Unmarshal(payload, &e); err != nil {
			b.Fatal(err)
		}
	}
}
