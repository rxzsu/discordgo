package discordgo

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// interactionTestSeed is a fixed Ed25519 seed so every signature in this file
// is reproducible across runs (mirrors a real application key pair).
var interactionTestSeed = [32]byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
	0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
}

var (
	interactionTestPrivKey = ed25519.NewKeyFromSeed(interactionTestSeed[:])
	interactionTestPubKey  = interactionTestPrivKey.Public().(ed25519.PublicKey)
)

// interactionTestOtherSeed is a second fixed seed; signatures produced by it
// are structurally valid but must not verify against interactionTestPubKey.
var interactionTestOtherSeed = [32]byte{
	0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8,
	0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0,
	0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8,
	0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf, 0xc0,
}

var interactionTestOtherPriv = ed25519.NewKeyFromSeed(interactionTestOtherSeed[:])

// signedInteractionRequest builds a POST request with a valid Ed25519
// signature over the given timestamp and body. mutate runs after signing, so
// tests can tamper with headers or the body to produce invalid requests.
func signedInteractionRequest(t *testing.T, body []byte, timestamp string, mutate func(*http.Request)) *http.Request {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(body))
	var msg bytes.Buffer
	msg.WriteString(timestamp)
	msg.Write(body)
	sig := ed25519.Sign(interactionTestPrivKey, msg.Bytes())
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", timestamp)
	if mutate != nil {
		mutate(req)
	}
	return req
}

// nowTimestamp returns the current unix time as the signature timestamp.
func nowTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// decodeInteractionResponse decodes a JSON interaction response body.
func decodeInteractionResponse(t *testing.T, rec *httptest.ResponseRecorder) *InteractionResponse {
	t.Helper()
	var resp InteractionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response body %q: %v", rec.Body.String(), err)
	}
	return &resp
}

// pingBody is a minimal PING interaction payload from Discord.
var pingBody = []byte(`{"id":"1","application_id":"2","type":1,"token":"token","version":1,"user":{"id":"3","username":"tester"}}`)

// commandBody is a minimal application command interaction payload.
var commandBody = []byte(`{"id":"10","application_id":"2","type":2,"token":"token","version":1,"guild_id":"55","channel_id":"66","data":{"id":"77","name":"ping"},"member":{"user":{"id":"3","username":"tester"}}}`)

func TestInteractionPublicKeyFromHex(t *testing.T) {
	valid := hex.EncodeToString(interactionTestPubKey)

	t.Run("valid", func(t *testing.T) {
		key, err := InteractionPublicKeyFromHex(valid)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(key, interactionTestPubKey) {
			t.Fatalf("expected %x, got %x", interactionTestPubKey, key)
		}
	})

	t.Run("invalid hex", func(t *testing.T) {
		if _, err := InteractionPublicKeyFromHex("not-hex!"); err == nil {
			t.Fatal("expected error for invalid hex")
		}
	})

	t.Run("wrong length", func(t *testing.T) {
		if _, err := InteractionPublicKeyFromHex("aabbcc"); err == nil {
			t.Fatal("expected error for wrong key length")
		}
	})
}

func TestInteractionServerPing(t *testing.T) {
	is := NewInteractionServer(interactionTestPubKey)

	var commandCalled bool
	is.OnApplicationCommand(func(i *Interaction) *InteractionResponse {
		commandCalled = true
		return &InteractionResponse{Type: InteractionResponsePong}
	})

	rec := httptest.NewRecorder()
	is.ServeHTTP(rec, signedInteractionRequest(t, pingBody, nowTimestamp(), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json content type, got %q", ct)
	}
	resp := decodeInteractionResponse(t, rec)
	if resp.Type != InteractionResponsePong {
		t.Fatalf("expected PONG response, got type %d", resp.Type)
	}
	if commandCalled {
		t.Fatal("PING must not reach interaction handlers")
	}
}

func TestInteractionServerSignatureRejections(t *testing.T) {
	ts := nowTimestamp()

	tests := []struct {
		name   string
		body   []byte
		mutate func(*http.Request)
	}{
		{
			name: "missing signature header",
			body: pingBody,
			mutate: func(r *http.Request) {
				r.Header.Del("X-Signature-Ed25519")
			},
		},
		{
			name: "malformed signature hex",
			body: pingBody,
			mutate: func(r *http.Request) {
				r.Header.Set("X-Signature-Ed25519", "zz-not-hex")
			},
		},
		{
			name: "signature of wrong length",
			body: pingBody,
			mutate: func(r *http.Request) {
				r.Header.Set("X-Signature-Ed25519", strings.Repeat("ab", 10))
			},
		},
		{
			name: "signature by different key",
			body: pingBody,
			mutate: func(r *http.Request) {
				var msg bytes.Buffer
				msg.WriteString(r.Header.Get("X-Signature-Timestamp"))
				msg.Write(pingBody)
				sig := ed25519.Sign(interactionTestOtherPriv, msg.Bytes())
				r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
			},
		},
		{
			name: "modified body",
			body: pingBody,
			mutate: func(r *http.Request) {
				r.Body = io.NopCloser(strings.NewReader(`{"id":"1","type":2,"tampered":true}`))
			},
		},
		{
			name: "missing timestamp header",
			body: pingBody,
			mutate: func(r *http.Request) {
				r.Header.Del("X-Signature-Timestamp")
			},
		},
		{
			name: "malformed timestamp header",
			body: pingBody,
			mutate: func(r *http.Request) {
				r.Header.Set("X-Signature-Timestamp", "yesterday")
			},
		},
		{
			name: "stale timestamp",
			body: pingBody,
			// Signed over the old timestamp, so only the freshness window
			// rejects the request.
			mutate: func(r *http.Request) {
				old := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
				var msg bytes.Buffer
				msg.WriteString(old)
				msg.Write(pingBody)
				sig := ed25519.Sign(interactionTestPrivKey, msg.Bytes())
				r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
				r.Header.Set("X-Signature-Timestamp", old)
			},
		},
		{
			name: "timestamp too far in the future",
			body: pingBody,
			mutate: func(r *http.Request) {
				future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
				var msg bytes.Buffer
				msg.WriteString(future)
				msg.Write(pingBody)
				sig := ed25519.Sign(interactionTestPrivKey, msg.Bytes())
				r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
				r.Header.Set("X-Signature-Timestamp", future)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			is := NewInteractionServer(interactionTestPubKey)
			rec := httptest.NewRecorder()
			is.ServeHTTP(rec, signedInteractionRequest(t, tc.body, ts, tc.mutate))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d (body: %q)", rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("freshness check can be disabled", func(t *testing.T) {
		is := NewInteractionServer(interactionTestPubKey)
		is.MaxTimestampAge = -1
		old := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
		rec := httptest.NewRecorder()
		is.ServeHTTP(rec, signedInteractionRequest(t, pingBody, old, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 with freshness check disabled, got %d", rec.Code)
		}
	})
}

func TestInteractionServerDispatch(t *testing.T) {
	ts := nowTimestamp()

	slashBody := []byte(`{"id":"10","type":2,"token":"t","version":1,"data":{"id":"77","name":"ping"}}`)
	componentBody := []byte(`{"id":"11","type":3,"token":"t","version":1,"data":{"custom_id":"btn","component_type":2}}`)
	autocompleteBody := []byte(`{"id":"12","type":4,"token":"t","version":1,"data":{"id":"77","name":"find"}}`)
	modalBody := []byte(`{"id":"13","type":5,"token":"t","version":1,"data":{"custom_id":"modal","components":[]}}`)
	unknownBody := []byte(`{"id":"14","type":99,"token":"t","version":1}`)

	tests := []struct {
		name     string
		body     []byte
		register func(*InteractionServer) *bool
		wantType InteractionResponseType
	}{
		{
			name: "application command",
			body: slashBody,
			register: func(is *InteractionServer) *bool {
				called := false
				is.OnApplicationCommand(func(i *Interaction) *InteractionResponse {
					called = true
					if i.Type != InteractionApplicationCommand {
						t.Errorf("expected command interaction, got %s", i.Type)
					}
					if data, ok := i.Data.(ApplicationCommandInteractionData); !ok || data.Name != "ping" {
						t.Errorf("expected command data name %q, got %#v", "ping", i.Data)
					}
					return &InteractionResponse{
						Type: InteractionResponseChannelMessageWithSource,
						Data: &InteractionResponseData{Content: "pong"},
					}
				})
				return &called
			},
			wantType: InteractionResponseChannelMessageWithSource,
		},
		{
			name: "message component",
			body: componentBody,
			register: func(is *InteractionServer) *bool {
				called := false
				is.OnComponent(func(i *Interaction) *InteractionResponse {
					called = true
					return &InteractionResponse{Type: InteractionResponseDeferredMessageUpdate}
				})
				return &called
			},
			wantType: InteractionResponseDeferredMessageUpdate,
		},
		{
			name: "autocomplete",
			body: autocompleteBody,
			register: func(is *InteractionServer) *bool {
				called := false
				is.OnAutocomplete(func(i *Interaction) *InteractionResponse {
					called = true
					return &InteractionResponse{
						Type: InteractionApplicationCommandAutocompleteResult,
						Data: &InteractionResponseData{Choices: []*ApplicationCommandOptionChoice{{Name: "a", Value: "a"}}},
					}
				})
				return &called
			},
			wantType: InteractionApplicationCommandAutocompleteResult,
		},
		{
			name: "modal submit",
			body: modalBody,
			register: func(is *InteractionServer) *bool {
				called := false
				is.OnModalSubmit(func(i *Interaction) *InteractionResponse {
					called = true
					return &InteractionResponse{Type: InteractionResponseChannelMessageWithSource}
				})
				return &called
			},
			wantType: InteractionResponseChannelMessageWithSource,
		},
		{
			name: "unknown type falls back",
			body: unknownBody,
			register: func(is *InteractionServer) *bool {
				called := false
				is.OnFallback(func(i *Interaction) *InteractionResponse {
					called = true
					return &InteractionResponse{Type: InteractionResponseDeferredChannelMessageWithSource}
				})
				return &called
			},
			wantType: InteractionResponseDeferredChannelMessageWithSource,
		},
		{
			name: "nil dedicated handler falls back",
			body: slashBody,
			register: func(is *InteractionServer) *bool {
				called := false
				is.OnFallback(func(i *Interaction) *InteractionResponse {
					called = true
					return &InteractionResponse{Type: InteractionResponseDeferredChannelMessageWithSource}
				})
				return &called
			},
			wantType: InteractionResponseDeferredChannelMessageWithSource,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			is := NewInteractionServer(interactionTestPubKey)
			called := tc.register(is)

			rec := httptest.NewRecorder()
			is.ServeHTTP(rec, signedInteractionRequest(t, tc.body, ts, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (body: %q)", rec.Code, rec.Body.String())
			}
			if !*called {
				t.Fatal("expected handler to be called")
			}
			resp := decodeInteractionResponse(t, rec)
			if resp.Type != tc.wantType {
				t.Fatalf("expected response type %d, got %d", tc.wantType, resp.Type)
			}
		})
	}

	t.Run("decoded interaction fields survive the round trip", func(t *testing.T) {
		is := NewInteractionServer(interactionTestPubKey)
		is.OnApplicationCommand(func(i *Interaction) *InteractionResponse {
			if i.ID != "10" || i.GuildID != "55" || i.ChannelID != "66" || i.Token != "token" {
				t.Errorf("unexpected interaction fields: %#v", i)
			}
			if i.Member == nil || i.Member.User == nil || i.Member.User.Username != "tester" {
				t.Errorf("expected member user to be decoded, got %#v", i.Member)
			}
			return &InteractionResponse{Type: InteractionResponseChannelMessageWithSource}
		})

		rec := httptest.NewRecorder()
		is.ServeHTTP(rec, signedInteractionRequest(t, commandBody, ts, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})
}

func TestInteractionServerNoResponse(t *testing.T) {
	ts := nowTimestamp()

	tests := []struct {
		name       string
		autoDefer  bool
		handler    InteractionHandler
		wantStatus int
		wantType   InteractionResponseType // 0 when a bare 204 is expected
	}{
		{
			name:       "no handler, no auto defer",
			autoDefer:  false,
			handler:    nil,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "no handler, auto defer",
			autoDefer:  true,
			handler:    nil,
			wantStatus: http.StatusOK,
			wantType:   InteractionResponseDeferredChannelMessageWithSource,
		},
		{
			name:       "nil handler response, no auto defer",
			autoDefer:  false,
			handler:    func(*Interaction) *InteractionResponse { return nil },
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "nil handler response, auto defer",
			autoDefer:  true,
			handler:    func(*Interaction) *InteractionResponse { return nil },
			wantStatus: http.StatusOK,
			wantType:   InteractionResponseDeferredChannelMessageWithSource,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			is := NewInteractionServer(interactionTestPubKey)
			is.AutoDefer = tc.autoDefer
			if tc.handler != nil {
				is.OnApplicationCommand(tc.handler)
			}

			rec := httptest.NewRecorder()
			is.ServeHTTP(rec, signedInteractionRequest(t, commandBody, ts, nil))

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d (body: %q)", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantType != 0 {
				resp := decodeInteractionResponse(t, rec)
				if resp.Type != tc.wantType {
					t.Fatalf("expected deferred response type %d, got %d", tc.wantType, resp.Type)
				}
			}
		})
	}
}

func TestInteractionServerPanicRecovery(t *testing.T) {
	ts := nowTimestamp()
	panicking := func(*Interaction) *InteractionResponse { panic("boom") }

	t.Run("recovered", func(t *testing.T) {
		is := NewInteractionServer(interactionTestPubKey)
		is.OnApplicationCommand(panicking)

		rec := httptest.NewRecorder()
		is.ServeHTTP(rec, signedInteractionRequest(t, commandBody, ts, nil))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("propagated", func(t *testing.T) {
		is := NewInteractionServer(interactionTestPubKey)
		is.RecoverPanics = false
		is.OnApplicationCommand(panicking)

		defer func() {
			if recover() == nil {
				t.Fatal("expected panic to propagate with RecoverPanics disabled")
			}
		}()

		rec := httptest.NewRecorder()
		is.ServeHTTP(rec, signedInteractionRequest(t, commandBody, ts, nil))
	})
}

func TestInteractionServerBadRequestHandling(t *testing.T) {
	ts := nowTimestamp()

	tests := []struct {
		name       string
		server     *InteractionServer
		request    *http.Request
		wantStatus int
	}{
		{
			name:       "GET method",
			server:     NewInteractionServer(interactionTestPubKey),
			request:    httptest.NewRequest(http.MethodGet, "/interactions", nil),
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "PUT method",
			server:     NewInteractionServer(interactionTestPubKey),
			request:    httptest.NewRequest(http.MethodPut, "/interactions", nil),
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "invalid JSON body with valid signature",
			server:     NewInteractionServer(interactionTestPubKey),
			request:    signedInteractionRequest(t, []byte(`{"type":`), ts, nil),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "body larger than the limit",
			server:     NewInteractionServer(interactionTestPubKey),
			request:    signedInteractionRequest(t, bytes.Repeat([]byte("x"), int(InteractionServerMaxBodySize)+1), ts, nil),
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "no public key configured",
			server:     NewInteractionServer(nil),
			request:    httptest.NewRequest(http.MethodPost, "/interactions", bytes.NewReader(pingBody)),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.server.ServeHTTP(rec, tc.request)
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d (body: %q)", tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("405 advertises POST", func(t *testing.T) {
		is := NewInteractionServer(interactionTestPubKey)
		rec := httptest.NewRecorder()
		is.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/interactions", nil))
		if allow := rec.Header().Get("Allow"); allow != http.MethodPost {
			t.Fatalf("expected Allow: POST, got %q", allow)
		}
	})

	t.Run("negative MaxBodySize disables the limit", func(t *testing.T) {
		is := NewInteractionServer(interactionTestPubKey)
		is.MaxBodySize = -1

		big := bytes.Repeat([]byte("x"), int(InteractionServerMaxBodySize)+1)
		rec := httptest.NewRecorder()
		is.ServeHTTP(rec, signedInteractionRequest(t, big, ts, nil))
		// The oversized body is not rejected with 413; it flows through
		// signature verification and fails JSON decoding instead (the body is
		// not a valid interaction).
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for oversized body with disabled limit, got %d (body: %q)", rec.Code, rec.Body.String())
		}
	})
}

func TestInteractionServerMultipartResponse(t *testing.T) {
	ts := nowTimestamp()

	is := NewInteractionServer(interactionTestPubKey)
	is.OnApplicationCommand(func(i *Interaction) *InteractionResponse {
		return &InteractionResponse{
			Type: InteractionResponseChannelMessageWithSource,
			Data: &InteractionResponseData{
				Content: "with file",
				Files: []*File{{
					Name:        "hello.txt",
					ContentType: "text/plain",
					Reader:      strings.NewReader("hello from the interaction server"),
				}},
			},
		}
	})

	rec := httptest.NewRecorder()
	is.ServeHTTP(rec, signedInteractionRequest(t, commandBody, ts, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %q)", rec.Code, rec.Body.String())
	}

	mediaType, params, err := mime.ParseMediaType(rec.Header().Get("Content-Type"))
	if err != nil {
		t.Fatalf("failed to parse content type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("expected multipart response for files, got %q", mediaType)
	}

	mr := multipart.NewReader(rec.Body, params["boundary"])
	parts := 0
	var payloadJSON, fileContent string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read multipart part: %v", err)
		}
		data, err := io.ReadAll(p)
		if err != nil {
			t.Fatalf("failed to read part body: %v", err)
		}
		switch p.FormName() {
		case "payload_json":
			var resp InteractionResponse
			if err := json.Unmarshal(data, &resp); err != nil {
				t.Fatalf("failed to decode payload_json: %v", err)
			}
			if resp.Type != InteractionResponseChannelMessageWithSource || resp.Data == nil || resp.Data.Content != "with file" {
				t.Errorf("unexpected payload_json content: %#v", resp)
			}
			payloadJSON = string(data)
		case "files[0]":
			fileContent = string(data)
		default:
			t.Errorf("unexpected multipart part %q", p.FormName())
		}
		parts++
	}

	if parts != 2 {
		t.Fatalf("expected 2 multipart parts, got %d", parts)
	}
	if payloadJSON == "" {
		t.Fatal("payload_json part missing")
	}
	if fileContent != "hello from the interaction server" {
		t.Fatalf("unexpected file content %q", fileContent)
	}
}

func TestInteractionServerServesThroughHTTPServer(t *testing.T) {
	// Exercises the server through a real net/http server to verify it
	// behaves as a drop-in http.Handler (MaxBytesReader wired to the real
	// ResponseWriter, header handling, etc).
	is := NewInteractionServer(interactionTestPubKey)
	srv := httptest.NewServer(is)
	defer srv.Close()

	ts := nowTimestamp()
	var msg bytes.Buffer
	msg.WriteString(ts)
	msg.Write(pingBody)
	sig := ed25519.Sign(interactionTestPrivKey, msg.Bytes())

	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(pingBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var got InteractionResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got.Type != InteractionResponsePong {
		t.Fatalf("expected PONG, got type %d", got.Type)
	}
}
