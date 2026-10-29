package discordgo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func BenchmarkBucketIDFromURL(b *testing.B) {
	url := "https://discord.com/api/v9/channels/12345/messages?limit=50&after=999"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BucketIDFromURL(url)
	}
}

func BenchmarkBucketIDFromURLSplitN(b *testing.B) {
	url := "https://discord.com/api/v9/channels/12345/messages?limit=50&after=999"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = strings.SplitN(url, "?", 2)[0]
	}
}

func BenchmarkRequestPathNoOpts(b *testing.B) {
	// Measures the fixed per-request overhead of building and executing
	// a REST request against a stub transport.
	s, _ := New("Bot benchmark-token")
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{},
			Body:       http.NoBody,
			Request:    r,
		}, nil
	})}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := s.RequestWithBucketID("GET", "https://discord.com/api/v9/users/123", nil, "")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOnEventDispatch(b *testing.B) {
	s, _ := New("Bot benchmark-token")
	payload := []byte(`{"t":"MESSAGE_CREATE","s":1,"op":0,"d":{"id":"1","channel_id":"c","author":{"id":"u","username":"n"},"content":"hello world"}}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.onEvent(int(websocket.MessageText), payload)
	}
}

func BenchmarkStateMessageAdd(b *testing.B) {
	st := NewState()
	ch := &Channel{ID: "c1", Messages: []*Message{}}
	st.channelMap["c1"] = ch

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = st.MessageAdd(&Message{ID: string(rune('a' + i%26)), ChannelID: "c1"})
	}
}
