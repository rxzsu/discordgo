// This file implements an HTTP server for Discord interactions (the "webhook
// interactions mode" of the Discord API). Instead of receiving interaction
// events over the gateway websocket, the application exposes an HTTP endpoint;
// Discord POSTs every interaction to that endpoint and reads the interaction
// response directly from the HTTP response. This allows bots to run without a
// persistent gateway connection.
//
// Every request Discord sends is signed with the application's Ed25519 key
// pair (X-Signature-Ed25519 / X-Signature-Timestamp headers) as documented at
// https://discord.com/developers/docs/interactions/receiving-and-responding#security-and-authorization
// and must be answered within three seconds (see InteractionDeadline).
//
// Usage:
//
//	key, err := discordgo.InteractionPublicKeyFromHex(os.Getenv("DISCORD_PUBLIC_KEY"))
//	if err != nil { ... }
//	is := discordgo.NewInteractionServer(key).
//	    OnApplicationCommand(func(i *discordgo.Interaction) *discordgo.InteractionResponse {
//	        return &discordgo.InteractionResponse{
//	            Type: discordgo.InteractionResponseChannelMessageWithSource,
//	            Data: &discordgo.InteractionResponseData{Content: "ping!"},
//	        }
//	    })
//	http.ListenAndServe(":8080", is) // or http.Handle("/interactions", is)

package discordgo

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const (
	// InteractionServerMaxBodySize is the default limit applied to incoming
	// interaction request bodies. Discord interaction payloads are small; the
	// limit only exists to bound memory usage from unauthenticated requests.
	InteractionServerMaxBodySize int64 = 2 << 20 // 2 MiB

	// InteractionServerMaxTimestampAge is the default maximum age (and future
	// skew) accepted for the signed X-Signature-Timestamp header. Older
	// signatures are rejected to mitigate replay attacks.
	InteractionServerMaxTimestampAge = 5 * time.Minute
)

// InteractionHandler processes a decoded interaction and returns the response
// that is written back to Discord as the HTTP response body.
//
// Returning a nil *InteractionResponse means "no immediate response": with
// InteractionServer.AutoDefer the server answers with a deferred
// channel-message ACK (type 5) so followup messages can be sent through the
// REST API later, otherwise a bare 204 No Content is written.
type InteractionHandler func(i *Interaction) *InteractionResponse

// InteractionServer is a standalone http.Handler that receives Discord
// interactions over HTTP (webhook interactions mode). It verifies the
// Ed25519 request signature, answers the PING handshake automatically and
// dispatches interactions to the handlers registered with the On* methods.
//
// The zero value is not usable; create servers with NewInteractionServer so
// safe defaults (body size limit, timestamp freshness check and panic
// recovery) are applied. InteractionServer is safe for concurrent use as long
// as handlers and configuration are not modified while requests are served.
type InteractionServer struct {
	// PublicKey is the application's public key from the developer portal
	// ("Interactions Endpoint URL" configuration page). Requests whose
	// signature does not verify against this key are rejected with 401.
	PublicKey ed25519.PublicKey

	// MaxBodySize limits the request body in bytes. Zero applies the default
	// (InteractionServerMaxBodySize); a negative value disables the limit.
	MaxBodySize int64

	// MaxTimestampAge is the maximum accepted age (and future skew) of the
	// signed X-Signature-Timestamp header. Zero applies the default
	// (InteractionServerMaxTimestampAge); a negative value disables the
	// freshness check.
	MaxTimestampAge time.Duration

	// RecoverPanics turns panics raised inside interaction handlers into HTTP
	// 500 responses instead of crashing the process. NewInteractionServer
	// enables it; set it to false on the zero value to propagate panics.
	RecoverPanics bool

	// AutoDefer responds with InteractionResponseDeferredChannelMessageWithSource
	// when no handler is registered for an interaction or when a handler
	// returns nil, enabling REST followups. Disabled (204 No Content) by default.
	AutoDefer bool

	// Handlers for each interaction type. A nil handler falls back to
	// FallbackHandler; when that is nil too, the default "no response"
	// behaviour (see AutoDefer) applies.
	ApplicationCommandHandler InteractionHandler
	MessageComponentHandler   InteractionHandler
	AutocompleteHandler       InteractionHandler
	ModalSubmitHandler        InteractionHandler
	FallbackHandler           InteractionHandler // used for any other interaction type
}

// NewInteractionServer creates an InteractionServer for the given application
// public key with safe defaults: a request body limit of
// InteractionServerMaxBodySize, a signature timestamp freshness window of
// InteractionServerMaxTimestampAge and panic recovery enabled.
func NewInteractionServer(key ed25519.PublicKey) *InteractionServer {
	return &InteractionServer{
		PublicKey:       key,
		MaxBodySize:     InteractionServerMaxBodySize,
		MaxTimestampAge: InteractionServerMaxTimestampAge,
		RecoverPanics:   true,
	}
}

// InteractionPublicKeyFromHex parses an application public key from the hex
// representation shown on the developer portal (General Information page) or
// returned by GET /oauth2/applications/@me.
func InteractionPublicKeyFromHex(hexKey string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode interaction public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("interaction public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

// OnApplicationCommand registers h for InteractionApplicationCommand
// interactions (slash commands and context menu commands).
func (is *InteractionServer) OnApplicationCommand(h InteractionHandler) *InteractionServer {
	is.ApplicationCommandHandler = h
	return is
}

// OnComponent registers h for InteractionMessageComponent interactions
// (buttons and select menus).
func (is *InteractionServer) OnComponent(h InteractionHandler) *InteractionServer {
	is.MessageComponentHandler = h
	return is
}

// OnAutocomplete registers h for InteractionApplicationCommandAutocomplete
// interactions. Autocomplete handlers must respond with
// InteractionApplicationCommandAutocompleteResult.
func (is *InteractionServer) OnAutocomplete(h InteractionHandler) *InteractionServer {
	is.AutocompleteHandler = h
	return is
}

// OnModalSubmit registers h for InteractionModalSubmit interactions.
func (is *InteractionServer) OnModalSubmit(h InteractionHandler) *InteractionServer {
	is.ModalSubmitHandler = h
	return is
}

// OnFallback registers h for interactions whose type has no dedicated handler
// registered (or whose dedicated handler is nil).
func (is *InteractionServer) OnFallback(h InteractionHandler) *InteractionServer {
	is.FallbackHandler = h
	return is
}

// handlerFor returns the handler registered for the given interaction type,
// falling back to FallbackHandler.
func (is *InteractionServer) handlerFor(t InteractionType) InteractionHandler {
	var h InteractionHandler
	switch t {
	case InteractionApplicationCommand:
		h = is.ApplicationCommandHandler
	case InteractionMessageComponent:
		h = is.MessageComponentHandler
	case InteractionApplicationCommandAutocomplete:
		h = is.AutocompleteHandler
	case InteractionModalSubmit:
		h = is.ModalSubmitHandler
	}
	if h == nil {
		h = is.FallbackHandler
	}
	return h
}

// ServeHTTP implements http.Handler. It verifies the Discord Ed25519 signature
// headers, decodes the interaction, answers PING pings automatically and
// writes the handler's InteractionResponse back as the HTTP response.
//
// Response status codes:
//
//	200 - interaction response written (JSON, or multipart when files are sent)
//	204 - handler returned nil and AutoDefer is disabled
//	400 - request body could not be read or decoded
//	401 - missing/invalid signature or stale signature timestamp
//	405 - request method is not POST
//	413 - request body exceeds MaxBodySize
//	500 - server has no public key configured, response could not be encoded,
//	      or an interaction handler panicked (with RecoverPanics enabled)
func (is *InteractionServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "interaction endpoint only accepts POST requests", http.StatusMethodNotAllowed)
		return
	}

	if len(is.PublicKey) == 0 {
		msglog(LogError, 2, "interaction server has no public key configured; refusing to serve interactions")
		http.Error(w, "interaction endpoint is not configured", http.StatusInternalServerError)
		return
	}

	maxBodySize := is.MaxBodySize
	bodyLimited := maxBodySize >= 0
	switch {
	case maxBodySize == 0:
		maxBodySize = InteractionServerMaxBodySize
	case maxBodySize < 0:
		maxBodySize = 0 // limit disabled; MaxBodySize negative never read below
	}

	if bodyLimited {
		// Fast pre-check for requests that declare their size up front; the
		// MaxBytesReader below catches chunked requests during signature
		// verification, which then reports the request as unverified.
		if r.ContentLength > 0 && r.ContentLength > maxBodySize {
			http.Error(w, "interaction request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	}

	if !VerifyInteraction(r, is.PublicKey) {
		msglog(LogWarning, 2, "interaction request failed Ed25519 signature verification")
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}

	if !is.timestampFresh(r) {
		msglog(LogWarning, 2, "interaction request signature timestamp is stale")
		http.Error(w, "invalid request signature timestamp", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		msglog(LogError, 2, "failed to read interaction request body: %v", err)
		http.Error(w, "unable to read request body", http.StatusBadRequest)
		return
	}

	var i Interaction
	if err = unmarshal(body, &i); err != nil {
		msglog(LogWarning, 2, "failed to decode interaction payload: %v", err)
		http.Error(w, "invalid interaction payload", http.StatusBadRequest)
		return
	}

	// Discord requires an immediate PONG for the PING handshake used when
	// registering the endpoint in the developer portal.
	if i.Type == InteractionPing {
		is.writeResponse(w, &InteractionResponse{Type: InteractionResponsePong})
		return
	}

	handler := is.handlerFor(i.Type)
	if handler == nil {
		msglog(LogInformational, 2, "no handler registered for interaction type %s", i.Type)
		is.writeNoResponse(w)
		return
	}

	if is.RecoverPanics {
		defer func() {
			if rec := recover(); rec != nil {
				msglog(LogError, 2, "interaction handler panicked: %v", rec)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
	}

	resp := handler(&i)
	if resp == nil {
		is.writeNoResponse(w)
		return
	}

	is.writeResponse(w, resp)
}

// timestampFresh reports whether the signed X-Signature-Timestamp header is
// within the configured freshness window.
func (is *InteractionServer) timestampFresh(r *http.Request) bool {
	if is.MaxTimestampAge < 0 {
		return true // freshness check disabled
	}
	maxAge := is.MaxTimestampAge
	if maxAge == 0 {
		maxAge = InteractionServerMaxTimestampAge
	}

	ts, err := strconv.ParseInt(r.Header.Get("X-Signature-Timestamp"), 10, 64)
	if err != nil {
		return false
	}

	delta := time.Since(time.Unix(ts, 0))
	return delta <= maxAge && delta >= -maxAge
}

// writeNoResponse writes the configured "no immediate response" answer.
func (is *InteractionServer) writeNoResponse(w http.ResponseWriter) {
	if is.AutoDefer {
		is.writeResponse(w, &InteractionResponse{Type: InteractionResponseDeferredChannelMessageWithSource})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeResponse writes an interaction response as JSON, or as multipart
// form-data when the response carries files. The multipart layout mirrors
// Discord's webhook file upload format: one payload_json part plus one
// files[N] part per file.
func (is *InteractionServer) writeResponse(w http.ResponseWriter, resp *InteractionResponse) {
	if resp.Data != nil && len(resp.Data.Files) > 0 {
		contentType, body, err := MultipartBodyWithJSON(resp, resp.Data.Files)
		if err != nil {
			msglog(LogError, 2, "failed to encode multipart interaction response: %v", err)
			http.Error(w, "failed to encode response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}

	body, err := Marshal(resp)
	if err != nil {
		msglog(LogError, 2, "failed to encode interaction response: %v", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
