// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file contains low level functions for interacting with the Discord
// data websocket interface.

package discordgo

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/adler32"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// RFC 1950 header constants: CMF must select the deflate method with a
// window no larger than 32K (mirrors compress/zlib's unexported values).
const (
	zlibDeflate   = 8
	zlibMaxWindow = 7
)

// ErrWSAlreadyOpen is thrown when you attempt to open
// a websocket that already is open.
var ErrWSAlreadyOpen = errors.New("web socket already opened")

// ErrWSNotFound is thrown when you attempt to use a websocket
// that doesn't exist
var ErrWSNotFound = errors.New("no websocket connection exists")

// wsHandshakeTimeout bounds the READY/RESUMED handshake reads in Open so
// a stalled gateway connection cannot hold the session lock forever.
const wsHandshakeTimeout = 30 * time.Second

// ErrWSNotReady is returned by Open when the gateway handshake ended
// with a reconnect request (op 7 / op 9) instead of READY or RESUMED.
// The reconnect loop treats it like any other Open failure and retries.
var ErrWSNotReady = errors.New("gateway session is not ready, reconnect requested")

// ErrWSShardBounds is thrown when you try to use a shard ID that is
// more than the total shard count
var ErrWSShardBounds = errors.New("ShardID must be less than ShardCount")

type resumePacket struct {
	Op   int `json:"op"`
	Data struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Sequence  int64  `json:"seq"`
	} `json:"d"`
}

// wsZlibStream is a minimal RFC 1950 (zlib) reader that keeps its deflate
// decompressor, adler32 digest and source alive for the whole session,
// resetting them per message. The gateway emits one independent zlib stream
// per binary frame, so a reset runs on every message; compress/zlib's
// Resetter path would additionally re-zero the reader state and allocate a
// fresh adler32 digest on each of those resets, which this type avoids.
// Behavior matches compress/zlib: the same header validation, the same
// trailing-checksum verification, and the same error values (ErrHeader,
// ErrDictionary, ErrChecksum) for malformed input.
type wsZlibStream struct {
	src     bytes.Reader
	flateR  io.ReadCloser
	adler   hash.Hash32
	scratch [4]byte
	hdr     [2]byte
	err     error
}

// reset primes the stream to decode one complete zlib stream from message.
func (w *wsZlibStream) reset(message []byte) error {
	w.err = nil
	w.src.Reset(message)

	// RFC 1950 section 2.2 header: CMF, FLG. The same validation
	// compress/zlib performs (deflate method, window size <= 32K, header
	// checksum). FDICT streams require a preset dictionary that this
	// library never supplies; compress/zlib reports ErrDictionary there,
	// so that value is reproduced. The header buffer lives in the struct
	// (not a local): a local array handed to io.ReadFull escapes and
	// would heap-allocate on every frame.
	if _, err := io.ReadFull(&w.src, w.hdr[:]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		w.err = err
		return err
	}
	if w.hdr[0]&0x0f != zlibDeflate || w.hdr[0]>>4 > zlibMaxWindow || binary.BigEndian.Uint16(w.hdr[:])%31 != 0 {
		w.err = zlib.ErrHeader
		return zlib.ErrHeader
	}
	if w.hdr[1]&0x20 != 0 {
		w.err = zlib.ErrDictionary
		return zlib.ErrDictionary
	}

	if w.flateR == nil {
		w.flateR = flate.NewReader(&w.src)
	} else if err := w.flateR.(flate.Resetter).Reset(&w.src, nil); err != nil {
		w.err = err
		return err
	}
	w.adler.Reset()
	return nil
}

func (w *wsZlibStream) Read(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.flateR.Read(p)
	if n > 0 {
		_, _ = w.adler.Write(p[:n])
	}
	if err != io.EOF {
		if err != nil {
			w.err = err
		}
		return n, err
	}
	// Stream finished: consume and verify the trailing checksum, then
	// report EOF exactly like compress/zlib does.
	if _, w.err = io.ReadFull(&w.src, w.scratch[:]); w.err != nil {
		if w.err == io.EOF {
			w.err = io.ErrUnexpectedEOF
		}
		return n, w.err
	}
	if binary.BigEndian.Uint32(w.scratch[:]) != w.adler.Sum32() {
		w.err = zlib.ErrChecksum
		return n, zlib.ErrChecksum
	}
	w.err = io.EOF
	return n, io.EOF
}

func (w *wsZlibStream) Close() error {
	if w.flateR != nil {
		return w.flateR.Close()
	}
	return nil
}

// Open creates a websocket connection to Discord.
// See: https://discord.com/developers/docs/topics/gateway#connecting
func (s *Session) Open() error {
	return s.OpenContext(context.Background())
}

// OpenContext connects to Discord using ctx for gateway discovery, dialing,
// and the initial handshake. After it returns successfully, cancelling ctx
// does not close the session; call Close to disconnect.
func (s *Session) OpenContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.log(LogInformational, "called")

	var err error

	// Prevent Open or other major Session functions from
	// being called while Open is still running.
	s.Lock()
	defer s.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	// If the websock is already open, bail out here.
	if s.wsConn != nil {
		return ErrWSAlreadyOpen
	}

	sequence := atomic.LoadInt64(s.sequence)

	var gateway string
	// Get the gateway to use for the Websocket connection
	if sequence != 0 && s.sessionID != "" && s.resumeGatewayURL != "" {
		s.log(LogDebug, "using resume gateway %s", s.resumeGatewayURL)
		gateway = s.resumeGatewayURL
	} else {
		if s.gateway == "" {
			s.gateway, err = s.Gateway(WithContext(ctx))
			if err != nil {
				return err
			}
		}

		gateway = s.gateway
	}

	// Add the version and encoding to the URL
	gateway += "?v=" + APIVersion + "&encoding=json"

	// Connect to the Gateway
	s.log(LogInformational, "connecting to gateway %s", gateway)
	dialCtx, cancelDial := context.WithTimeout(ctx, s.DialTimeout)
	defer cancelDial()
	header := http.Header{}
	header.Add("accept-encoding", "zlib")
	s.wsConn, _, err = websocket.Dial(dialCtx, gateway, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		s.log(LogError, "error connecting to gateway %s, %s", s.gateway, err)
		s.gateway = "" // clear cached gateway
		s.wsConn = nil // Just to be safe.
		return err
	}

	defer func() {
		// because of this, all code below must set err to the error
		// when exiting with an error :)  Maybe someone has a better
		// way :)
		if err != nil && s.wsConn != nil {
			_ = s.wsConn.CloseNow()
			s.wsConn = nil
		}
	}()

	// The first response from Discord should be an Op 10 (Hello) Packet.
	// When processed by onEvent the heartbeat goroutine will be started.
	// The read is bounded: a gateway that accepts the TCP connection but
	// stalls must not hold s.Lock (and therefore Close) forever.
	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, wsHandshakeTimeout)
	mt, m, err := s.wsConn.Read(handshakeCtx)
	cancelHandshake()
	if err != nil {
		return err
	}
	e, err := s.onEvent(int(mt), m)
	if err != nil {
		return err
	}
	if e.Operation != 10 {
		err = fmt.Errorf("expecting Op 10, got Op %d instead", e.Operation)
		return err
	}
	s.log(LogInformational, "Op 10 Hello Packet received from Discord")
	s.LastHeartbeatAck = time.Now().UTC()
	var h helloOp
	if err = json.Unmarshal(e.RawData, &h); err != nil {
		err = fmt.Errorf("error unmarshalling helloOp, %s", err)
		return err
	}

	// Now we send either an Op 2 Identity if this is a brand new
	// connection or Op 6 Resume if we are resuming an existing connection.
	if s.sessionID == "" && sequence == 0 {

		// Send Op 2 Identity Packet
		err = s.identifyContext(ctx)
		if err != nil {
			err = fmt.Errorf("error sending identify packet to gateway, %s: %w", s.gateway, err)
			return err
		}

	} else {

		// Send Op 6 Resume Packet
		p := resumePacket{}
		p.Op = 6
		p.Data.Token = s.Token
		p.Data.SessionID = s.sessionID
		p.Data.Sequence = sequence

		s.log(LogInformational, "sending resume packet to gateway")
		s.wsMutex.Lock()
		err = wsjson.Write(ctx, s.wsConn, p)
		s.wsMutex.Unlock()
		if err != nil {
			err = fmt.Errorf("error sending gateway resume packet, %s: %w", s.gateway, err)
			return err
		}

	}

	// A basic state is a hard requirement for Voice.
	// We create it here so the below READY/RESUMED packet can populate
	// the state :)
	// XXX: Move to New() func?
	if s.State == nil {
		state := NewState()
		state.TrackChannels = false
		state.TrackEmojis = false
		state.TrackMembers = false
		state.TrackRoles = false
		state.TrackVoice = false
		s.State = state
	}

	// Now Discord should send us a READY or RESUMED packet.
	handshakeCtx2, cancelHandshake2 := context.WithTimeout(ctx, wsHandshakeTimeout)
	mt, m, err = s.wsConn.Read(handshakeCtx2)
	cancelHandshake2()
	if err != nil {
		return err
	}
	e, err = s.onEvent(int(mt), m)
	if err != nil {
		return err
	}
	// op 7 (Reconnect) / op 9 (Invalid Session) close and reconnect via
	// CloseWithCode, which needs s.Lock — held by this very Open call.
	// Handling them inline here would deadlock the session forever.
	// Tear the connection down and let the reconnect loop retry.
	if e.Operation == 7 || e.Operation == 9 {
		s.log(LogInformational, "received op %d during handshake, closing for reconnect", e.Operation)
		_ = s.wsConn.CloseNow()
		s.wsConn = nil
		return ErrWSNotReady
	}
	if e.Type != `READY` && e.Type != `RESUMED` {
		// This is not fatal, but it does not follow their API documentation.
		s.log(LogWarning, "Expected READY/RESUMED, instead got:\n%#v\n", e)
	}
	s.log(LogInformational, "First Packet:\n%#v\n", e)

	s.log(LogInformational, "We are now connected to Discord, emitting connect event")
	s.handleEvent(connectEventType, &Connect{})

	// A VoiceConnections map is a hard requirement for Voice.
	// XXX: can this be moved to when opening a voice connection?
	if s.VoiceConnections == nil {
		s.log(LogInformational, "creating new VoiceConnections map")
		s.VoiceConnections = make(map[string]*VoiceConnection)
	}

	// Create listening chan outside of listen, as it needs to happen inside the
	// mutex lock and needs to exist before calling heartbeat and listen
	// go rountines.
	s.listening = make(chan interface{})

	// Start the fixed handler worker pool if configured.
	// NOTE: Open holds s.Lock for its whole duration.
	if s.DispatchWorkers > 0 && s.dispatchJobs == nil {
		s.startDispatchWorkers()
	}

	// Start sending heartbeats and reading messages from Discord.
	go s.heartbeat(s.wsConn, s.listening, h.HeartbeatInterval)
	go s.listen(s.wsConn, s.listening)

	s.log(LogInformational, "exiting")
	return nil
}

// listen polls the websocket connection for events, it will stop when the
// listening channel is closed, or an error occurs.
func (s *Session) listen(wsConn *websocket.Conn, listening <-chan interface{}) {

	s.log(LogInformational, "called")

	for {

		messageType, message, err := wsConn.Read(context.Background())

		if err != nil {

			// Detect if we have been closed manually. If a Close() has already
			// happened, the websocket we are listening on will be different to
			// the current session.
			s.RLock()
			sameConnection := s.wsConn == wsConn
			s.RUnlock()

			if sameConnection {

				s.log(LogWarning, "error reading from gateway %s websocket, %s", s.gateway, err)
				// There has been an error reading, close the websocket so that
				// OnDisconnect event is emitted.
				err := s.Close()
				if err != nil {
					s.log(LogWarning, "error closing session connection, %s", err)
				}

				s.log(LogInformational, "calling reconnect() now")
				s.reconnect()
			}

			return
		}

		select {

		case <-listening:
			return

		default:
			_, _ = s.onEvent(int(messageType), message)

		}
	}
}

type heartbeatOp struct {
	Op   int   `json:"op"`
	Data int64 `json:"d"`
}

type helloOp struct {
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

// FailedHeartbeatAcks is the number of heartbeat intervals to wait without
// receiving a heartbeat ACK before forcing a connection restart.
//
// NOTE: helloOp.HeartbeatInterval arrives as a millisecond value decoded
// into a time.Duration (i.e. stored in nanoseconds). Multiplying it by
// time.Millisecond therefore yields the real interval; keeping the extra
// time.Millisecond here is intentional, not a bug.
const FailedHeartbeatAcks time.Duration = 5 * time.Millisecond

// HeartbeatLatency returns the latency between heartbeat acknowledgement and heartbeat send.
func (s *Session) HeartbeatLatency() time.Duration {

	return s.LastHeartbeatAck.Sub(s.LastHeartbeatSent)

}

// heartbeat sends regular heartbeats to Discord so it knows the client
// is still connected.  If you do not send these heartbeats Discord will
// disconnect the websocket connection after a few seconds.
func (s *Session) heartbeat(wsConn *websocket.Conn, listening <-chan interface{}, heartbeatInterval time.Duration) {

	s.log(LogInformational, "called")

	if listening == nil || wsConn == nil {
		return
	}

	var err error
	ticker := time.NewTicker(heartbeatInterval * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-listening:
			return
		default:
		}
		s.RLock()
		last := s.LastHeartbeatAck
		s.RUnlock()
		sequence := atomic.LoadInt64(s.sequence)
		s.log(LogDebug, "sending gateway websocket heartbeat seq %d", sequence)
		s.wsMutex.Lock()
		s.LastHeartbeatSent = time.Now().UTC()
		err = wsjson.Write(context.Background(), wsConn, heartbeatOp{1, sequence})
		s.wsMutex.Unlock()
		if err != nil || time.Since(last) > (heartbeatInterval*FailedHeartbeatAcks) {
			// A heartbeat may have been scheduled just before Close. Never
			// let that old connection resurrect a deliberately closed session.
			select {
			case <-listening:
				return
			default:
			}
			if err != nil {
				s.log(LogError, "error sending heartbeat to gateway %s, %s", s.gateway, err)
			} else {
				s.log(LogError, "haven't gotten a heartbeat ACK in %v, triggering a reconnection", time.Now().UTC().Sub(last))
			}
			_ = s.Close()
			s.reconnect()
			return
		}
		s.Lock()
		if s.wsConn != wsConn || s.listening != listening {
			s.Unlock()
			return
		}
		s.DataReady = true
		s.Unlock()

		select {
		case <-ticker.C:
			// continue loop and send heartbeat
		case <-listening:
			return
		}
	}
}

// UpdateStatusData is provided to UpdateStatusComplex()
type UpdateStatusData struct {
	IdleSince  *int        `json:"since"`
	Activities []*Activity `json:"activities"`
	AFK        bool        `json:"afk"`
	Status     string      `json:"status"`
}

type updateStatusOp struct {
	Op   int              `json:"op"`
	Data UpdateStatusData `json:"d"`
}

func newUpdateStatusData(idle int, activityType ActivityType, name, url string) *UpdateStatusData {
	usd := &UpdateStatusData{
		Status: "online",
	}

	if idle > 0 {
		usd.IdleSince = &idle
	}

	if name != "" {
		usd.Activities = []*Activity{{
			Name: name,
			Type: activityType,
			URL:  url,
		}}
	}

	return usd
}

// UpdateGameStatus is used to update the user's status.
// If idle>0 then set status to idle.
// If name!="" then set game.
// if otherwise, set status to active, and no activity.
func (s *Session) UpdateGameStatus(idle int, name string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, ActivityTypeGame, name, ""))
}

// UpdateWatchStatus is used to update the user's watch status.
// If idle>0 then set status to idle.
// If name!="" then set movie/stream.
// if otherwise, set status to active, and no activity.
func (s *Session) UpdateWatchStatus(idle int, name string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, ActivityTypeWatching, name, ""))
}

// UpdateStreamingStatus is used to update the user's streaming status.
// If idle>0 then set status to idle.
// If name!="" then set game.
// If name!="" and url!="" then set the status type to streaming with the URL set.
// if otherwise, set status to active, and no game.
func (s *Session) UpdateStreamingStatus(idle int, name string, url string) (err error) {
	gameType := ActivityTypeGame
	if url != "" {
		gameType = ActivityTypeStreaming
	}
	return s.UpdateStatusComplex(*newUpdateStatusData(idle, gameType, name, url))
}

// UpdateListeningStatus is used to set the user to "Listening to..."
// If name!="" then set to what user is listening to
// Else, set user to active and no activity.
func (s *Session) UpdateListeningStatus(name string) (err error) {
	return s.UpdateStatusComplex(*newUpdateStatusData(0, ActivityTypeListening, name, ""))
}

// UpdateCustomStatus is used to update the user's custom status.
// If state!="" then set the custom status.
// Else, set user to active and remove the custom status.
func (s *Session) UpdateCustomStatus(state string) (err error) {
	data := UpdateStatusData{
		Status: "online",
	}

	if state != "" {
		// Discord requires a non-empty activity name, therefore we provide "Custom Status" as a placeholder.
		data.Activities = []*Activity{{
			Name:  "Custom Status",
			Type:  ActivityTypeCustom,
			State: state,
		}}
	}

	return s.UpdateStatusComplex(data)
}

// UpdateStatusComplex allows for sending the raw status update data untouched by discordgo.
func (s *Session) UpdateStatusComplex(usd UpdateStatusData) (err error) {
	// The comment does say "untouched by discordgo", but we might need to lie a bit here.
	// The Discord documentation lists `activities` as being nullable, but in practice this
	// doesn't seem to be the case. I had filed an issue about this at
	// https://github.com/discord/discord-api-docs/issues/2559, but as of writing this
	// haven't had any movement on it, so at this point I'm assuming this is an error,
	// and am fixing this bug accordingly. Because sending `null` for `activities` instantly
	// disconnects us, I think that disallowing it from being sent in `UpdateStatusComplex`
	// isn't that big of an issue.
	if usd.Activities == nil {
		usd.Activities = make([]*Activity, 0)
	}

	s.RLock()
	wsConn := s.wsConn
	s.RUnlock()
	if wsConn == nil {
		return ErrWSNotFound
	}

	s.wsMutex.Lock()
	err = wsjson.Write(context.Background(), wsConn, updateStatusOp{3, usd})
	s.wsMutex.Unlock()

	return
}

type requestGuildMembersData struct {
	// TODO: Deprecated. Use string instead of []string
	GuildIDs  []string  `json:"guild_id"`
	Query     *string   `json:"query,omitempty"`
	UserIDs   *[]string `json:"user_ids,omitempty"`
	Limit     int       `json:"limit"`
	Nonce     string    `json:"nonce,omitempty"`
	Presences bool      `json:"presences"`
}

type requestGuildMembersOp struct {
	Op   int                     `json:"op"`
	Data requestGuildMembersData `json:"d"`
}

// RequestGuildMembers requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Single Guild ID to request members of
// query     : String that username starts with, leave empty to return all members
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
func (s *Session) RequestGuildMembers(guildID, query string, limit int, nonce string, presences bool) error {
	return s.requestGuildMembers(requestGuildMembersData{
		GuildIDs:  []string{guildID},
		Query:     &query,
		Limit:     limit,
		Nonce:     nonce,
		Presences: presences,
	})
}

// RequestGuildMembersList requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Single Guild ID to request members of
// userIDs   : IDs of users to fetch
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
func (s *Session) RequestGuildMembersList(guildID string, userIDs []string, limit int, nonce string, presences bool) error {
	return s.requestGuildMembers(requestGuildMembersData{
		GuildIDs:  []string{guildID},
		UserIDs:   &userIDs,
		Limit:     limit,
		Nonce:     nonce,
		Presences: presences,
	})
}

// RequestGuildMembersBatch requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Slice of guild IDs to request members of
// query     : String that username starts with, leave empty to return all members
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
//
// Deprecated: use RequestGuildMembers instead.
func (s *Session) RequestGuildMembersBatch(guildIDs []string, query string, limit int, nonce string, presences bool) (err error) {
	data := requestGuildMembersData{
		GuildIDs:  guildIDs,
		Query:     &query,
		Limit:     limit,
		Nonce:     nonce,
		Presences: presences,
	}
	err = s.requestGuildMembers(data)
	return
}

// RequestGuildMembersBatchList requests guild members from the gateway
// The gateway responds with GuildMembersChunk events
// guildID   : Slice of guild IDs to request members of
// userIDs   : IDs of users to fetch
// limit     : Max number of items to return, or 0 to request all members matched
// nonce     : Nonce to identify the Guild Members Chunk response
// presences : Whether to request presences of guild members
//
// Deprecated: use RequestGuildMembersList instead.
func (s *Session) RequestGuildMembersBatchList(guildIDs []string, userIDs []string, limit int, nonce string, presences bool) (err error) {
	data := requestGuildMembersData{
		GuildIDs:  guildIDs,
		UserIDs:   &userIDs,
		Limit:     limit,
		Nonce:     nonce,
		Presences: presences,
	}
	err = s.requestGuildMembers(data)
	return
}

// GatewayWriteStruct allows for sending raw gateway structs over the gateway.
func (s *Session) GatewayWriteStruct(data interface{}) (err error) {
	s.RLock()
	wsConn := s.wsConn
	s.RUnlock()
	if wsConn == nil {
		return ErrWSNotFound
	}

	s.wsMutex.Lock()
	err = wsjson.Write(context.Background(), wsConn, data)
	s.wsMutex.Unlock()

	return err
}

func (s *Session) requestGuildMembers(data requestGuildMembersData) (err error) {
	s.log(LogInformational, "called")

	s.RLock()
	wsConn := s.wsConn
	s.RUnlock()
	if wsConn == nil {
		return ErrWSNotFound
	}

	s.wsMutex.Lock()
	err = wsjson.Write(context.Background(), wsConn, requestGuildMembersOp{8, data})
	s.wsMutex.Unlock()

	return
}

// onEvent is the "event handler" for all messages received on the
// Discord Gateway API websocket connection.
//
// If you use the AddHandler() function to register a handler for a
// specific event this function will pass the event along to that handler.
//
// If you use the AddHandler() function to register a handler for the
// "OnEvent" event then all events will be passed to that handler.
// gatewayEnvelope holds the fixed fields of a gateway payload, extracted by
// parseGatewayEnvelope without a full JSON decode.
type gatewayEnvelope struct {
	op   int
	seq  int64
	typ  string
	data []byte
}

// wsSkipWS returns the index of the first non-whitespace byte at or after i.
func wsSkipWS(p []byte, i int) int {
	for i < len(p) {
		switch p[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// wsSkipString skips a JSON string; i must point at the opening quote. It
// validates escape sequences and rejects raw control characters, so a
// successful return means the value really is a JSON string.
func wsSkipString(p []byte, i int) (int, bool) {
	i++ // opening quote
	for i < len(p) {
		c := p[i]
		switch {
		case c == '"':
			return i + 1, true
		case c == '\\':
			i++
			if i >= len(p) {
				return 0, false
			}
			switch p[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i++
			case 'u':
				for k := 1; k <= 4; k++ {
					if i+k >= len(p) {
						return 0, false
					}
					c := p[i+k]
					if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
						return 0, false
					}
				}
				i += 5
			default:
				return 0, false
			}
		case c < 0x20:
			return 0, false
		default:
			i++
		}
	}
	return 0, false
}

// wsSkipNumber skips a JSON number (int, float, exponent), strictly.
func wsSkipNumber(p []byte, i int) (int, bool) {
	if i < len(p) && p[i] == '-' {
		i++
	}
	if i >= len(p) || p[i] < '0' || p[i] > '9' {
		return 0, false
	}
	if p[i] == '0' {
		i++
	} else {
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			i++
		}
	}
	if i < len(p) && p[i] == '.' {
		i++
		if i >= len(p) || p[i] < '0' || p[i] > '9' {
			return 0, false
		}
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			i++
		}
	}
	if i < len(p) && (p[i] == 'e' || p[i] == 'E') {
		i++
		if i < len(p) && (p[i] == '+' || p[i] == '-') {
			i++
		}
		if i >= len(p) || p[i] < '0' || p[i] > '9' {
			return 0, false
		}
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			i++
		}
	}
	return i, true
}

// wsSkipLiteral skips one of the JSON literals true/false/null.
func wsSkipLiteral(p []byte, i int, lit string) (int, bool) {
	if i+len(lit) > len(p) || string(p[i:i+len(lit)]) != lit {
		return 0, false
	}
	return i + len(lit), true
}

// wsSkipComposite skips a JSON object or array starting at i, validating
// every element: keys and values are parsed (values recursively, so nested
// containers, strings and escaped quotes cannot desynchronize the structure).
func wsSkipComposite(p []byte, i int) (int, bool) {
	i = wsSkipWS(p, i)
	if i >= len(p) {
		return 0, false
	}
	isObj := p[i] == '{'
	if !isObj && p[i] != '[' {
		return 0, false
	}
	i = wsSkipWS(p, i+1)
	if i < len(p) && ((isObj && p[i] == '}') || (!isObj && p[i] == ']')) {
		return i + 1, true
	}
	for {
		if isObj {
			if i >= len(p) || p[i] != '"' {
				return 0, false
			}
			j, ok := wsSkipString(p, i)
			if !ok {
				return 0, false
			}
			i = wsSkipWS(p, j)
			if i >= len(p) || p[i] != ':' {
				return 0, false
			}
			i = wsSkipWS(p, i+1)
		}
		var ok bool
		i, ok = wsSkipValue(p, i)
		if !ok {
			return 0, false
		}
		i = wsSkipWS(p, i)
		if i >= len(p) {
			return 0, false
		}
		switch {
		case p[i] == ',':
			i = wsSkipWS(p, i+1)
		case isObj && p[i] == '}':
			return i + 1, true
		case !isObj && p[i] == ']':
			return i + 1, true
		default:
			return 0, false
		}
	}
}

// wsSkipValue skips any JSON value starting at i.
func wsSkipValue(p []byte, i int) (int, bool) {
	i = wsSkipWS(p, i)
	if i >= len(p) {
		return 0, false
	}
	switch c := p[i]; {
	case c == '"':
		return wsSkipString(p, i)
	case c == '{' || c == '[':
		return wsSkipComposite(p, i)
	case c == 't':
		return wsSkipLiteral(p, i, "true")
	case c == 'f':
		return wsSkipLiteral(p, i, "false")
	case c == 'n':
		return wsSkipLiteral(p, i, "null")
	default:
		return wsSkipNumber(p, i)
	}
}

// wsParseInt parses a JSON integer (optionally signed) at i.
func wsParseInt(p []byte, i int) (int64, int, bool) {
	neg := false
	if i < len(p) && p[i] == '-' {
		neg = true
		i++
	}
	if i >= len(p) || p[i] < '0' || p[i] > '9' {
		return 0, 0, false
	}
	var v int64
	// Leading zero must not be followed by more digits (strict JSON).
	if p[i] == '0' {
		i++
	} else {
		for i < len(p) && p[i] >= '0' && p[i] <= '9' {
			d := int64(p[i] - '0')
			if v > (1<<62)/10 {
				return 0, 0, false // overflow: fall back to the JSON decoder
			}
			v = v*10 + d
			i++
		}
	}
	if neg {
		v = -v
	}
	return v, i, true
}

// parseGatewayEnvelope extracts op, s, t and d from a gateway payload with a
// single byte scan instead of a full JSON decode. It returns ok=false for
// anything that is not exactly a flat JSON object with well-formed values, so
// the caller can fall back to the general decoder; on ok=true the payload has
// been fully validated structurally. ge.data aliases payload (see the
// aliasing note in onEvent).
func parseGatewayEnvelope(payload []byte) (ge gatewayEnvelope, ok bool) {
	n := len(payload)
	i := wsSkipWS(payload, 0)
	if i >= n || payload[i] != '{' {
		return ge, false
	}
	i = wsSkipWS(payload, i+1)
	if i < n && payload[i] == '}' {
		if wsSkipWS(payload, i+1) == n {
			return ge, true
		}
		return ge, false
	}

	var haveOp, haveSeq, haveTyp, haveData bool
	for {
		if i >= n || payload[i] != '"' {
			return ge, false
		}
		keyStart := i + 1
		j, ok := wsSkipString(payload, i)
		if !ok {
			return ge, false
		}
		key := payload[keyStart : j-1]

		i = wsSkipWS(payload, j)
		if i >= n || payload[i] != ':' {
			return ge, false
		}
		i = wsSkipWS(payload, i+1)

		switch string(key) {
		case "op":
			v, k, ok := wsParseInt(payload, i)
			if !ok || haveOp {
				return ge, false
			}
			ge.op, haveOp, i = int(v), true, k
		case "s":
			v, k, ok := wsParseInt(payload, i)
			if !ok || haveSeq {
				return ge, false
			}
			ge.seq, haveSeq, i = v, true, k
		case "t":
			if haveTyp {
				return ge, false
			}
			if i < n && payload[i] == 'n' {
				k, ok := wsSkipLiteral(payload, i, "null")
				if !ok {
					return ge, false
				}
				i = k
			} else {
				if i >= n || payload[i] != '"' {
					return ge, false
				}
				vs := i + 1
				k, ok := wsSkipString(payload, i)
				if !ok {
					return ge, false
				}
				// Event names are plain ASCII; an escaped name is legal but
				// never sent by the gateway — let the fallback decode it.
				if bytes.IndexByte(payload[vs:k-1], '\\') >= 0 {
					return ge, false
				}
				ge.typ, haveTyp, i = string(payload[vs:k-1]), true, k
			}
		case "d":
			k, ok := wsSkipValue(payload, i)
			if !ok || haveData {
				return ge, false
			}
			ge.data, haveData, i = payload[i:k], true, k
		default:
			k, ok := wsSkipValue(payload, i)
			if !ok {
				return ge, false
			}
			i = k
		}

		i = wsSkipWS(payload, i)
		if i >= n {
			return ge, false
		}
		switch payload[i] {
		case ',':
			i = wsSkipWS(payload, i+1)
		case '}':
			i++
			if wsSkipWS(payload, i) != n {
				return ge, false
			}
			return ge, true
		default:
			return ge, false
		}
	}
}

func (s *Session) onEvent(messageType int, message []byte) (*Event, error) {

	var (
		payload []byte
		err     error
	)

	// If this is a compressed message, uncompress it.
	if messageType == int(websocket.MessageBinary) {
		// One long-lived wsZlibStream per session: its deflate decompressor
		// (with 32KB window), adler32 digest and bytes.Reader source are all
		// reset in place per message. A fresh compress/zlib reader per
		// message would pay the reader allocation plus a 32KB window; zlib's
		// own Resetter path still allocates a fresh adler32 digest per
		// message, which wsZlibStream avoids.
		// Only the listen goroutine and Open call onEvent, so these
		// fields need no extra synchronization.
		s.zMu.Lock()
		zs, ok := s.zr.(*wsZlibStream)
		if !ok {
			zs = &wsZlibStream{adler: adler32.New()}
			s.zr = zs
		}
		if err2 := zs.reset(message); err2 != nil {
			s.zMu.Unlock()
			s.log(LogError, "error uncompressing websocket message, %s", err2)
			return nil, err2
		}

		s.zbuf.Reset()
		if _, err = io.Copy(&s.zbuf, s.zr); err != nil {
			s.zMu.Unlock()
			s.log(LogError, "error reading uncompressed websocket message, %s", err)
			return nil, err
		}
		payload = s.zbuf.Bytes()
		s.zMu.Unlock()
	} else {
		payload = message
	}

	// Decode the event into an Event struct.
	//
	// The gateway envelope (op/s/t/d) is extracted by a hand-rolled scan on
	// the hot path: a full decode parses and copies the entire "d" value into
	// RawData before the typed struct below parses the very same bytes again.
	// The scan validates the payload strictly and falls back to the general
	// decoder for anything unexpected, so acceptance is never looser than
	// Unmarshal. RawData produced by the fast path aliases payload (the
	// websocket read buffer or the reused zlib buffer): every in-library
	// consumer is synchronous, and the raw-handler path below copies RawData
	// before dispatching it, so nothing retains the aliased bytes.
	var e *Event
	if ge, ok := parseGatewayEnvelope(payload); ok {
		e = &Event{
			Operation: ge.op,
			Sequence:  ge.seq,
			Type:      ge.typ,
			RawData:   json.RawMessage(ge.data),
		}
	} else if err := Unmarshal(payload, &e); err != nil {
		s.log(LogError, "error decoding websocket message, %s", err)
		return e, err
	}

	if s.LogLevel >= LogDebug {
		s.log(LogDebug, "Op: %d, Seq: %d, Type: %s, Data: %s\n\n", e.Operation, e.Sequence, e.Type, string(e.RawData))
	}

	// Ping request.
	// Must respond with a heartbeat packet within 5 seconds
	if e.Operation == 1 {
		s.log(LogInformational, "sending heartbeat in response to Op1")
		s.RLock()
		wsConn := s.wsConn
		s.RUnlock()
		if wsConn == nil {
			return e, nil
		}
		s.wsMutex.Lock()
		err = wsjson.Write(context.Background(), wsConn, heartbeatOp{1, atomic.LoadInt64(s.sequence)})
		s.wsMutex.Unlock()
		if err != nil {
			s.log(LogError, "error sending heartbeat in response to Op1")
			return e, err
		}

		return e, nil
	}

	// Reconnect
	// Must immediately disconnect from gateway and reconnect to new gateway.
	if e.Operation == 7 {
		s.log(LogInformational, "Closing and reconnecting in response to Op7")
		_ = s.CloseWithCode(int(websocket.StatusServiceRestart))
		s.reconnect()
		return e, nil
	}

	// Invalid Session
	// Must respond with a Identify packet.
	if e.Operation == 9 {
		s.log(LogInformational, "Closing and reconnecting in response to Op9")
		_ = s.CloseWithCode(int(websocket.StatusServiceRestart))

		var resumable bool
		if err := json.Unmarshal(e.RawData, &resumable); err != nil {
			s.log(LogError, "error unmarshalling invalid session event, %s", err)
			return e, err
		}

		if !resumable {
			s.log(LogInformational, "Gateway session is not resumable, discarding its information")
			s.resumeGatewayURL = ""
			s.sessionID = ""
			atomic.StoreInt64(s.sequence, 0)
		}

		s.reconnect()
		return e, nil
	}

	if e.Operation == 10 {
		// Op10 is handled by Open()
		return e, nil
	}

	if e.Operation == 11 {
		s.Lock()
		s.LastHeartbeatAck = time.Now().UTC()
		s.Unlock()
		s.log(LogDebug, "got heartbeat ACK")
		return e, nil
	}

	// Do not try to Dispatch a non-Dispatch Message
	if e.Operation != 0 {
		// But we probably should be doing something with them.
		// TEMP
		if s.LogLevel >= LogWarning {
			s.log(LogWarning, "unknown Op: %d, Seq: %d, Type: %s, Data: %s, message: %s", e.Operation, e.Sequence, e.Type, string(e.RawData), string(message))
		}
		return e, nil
	}

	// Store the message sequence
	atomic.StoreInt64(s.sequence, e.Sequence)

	// Map event to registered event handlers and pass it along to any registered handlers.
	if eh, ok := registeredInterfaceProviders[e.Type]; ok {
		e.Struct = eh.New()

		// Attempt to unmarshal our event.
		if err = json.Unmarshal(e.RawData, e.Struct); err != nil {
			s.log(LogError, "error unmarshalling %s event, %s", e.Type, err)
		}

		// Send event to any registered event handlers for it's type.
		// Because the above doesn't cancel this, in case of an error
		// the struct could be partially populated or at default values.
		// However, most errors are due to a single field and I feel
		// it's better to pass along what we received than nothing at all.
		// TODO: Think about that decision :)
		// Either way, READY events must fire, even with errors.
		s.handleEvent(e.Type, e.Struct)
	} else {
		if s.LogLevel >= LogWarning {
			s.log(LogWarning, "unknown event: Op: %d, Seq: %d, Type: %s, Data: %s", e.Operation, e.Sequence, e.Type, string(e.RawData))
		}
	}

	// For legacy reasons, we send the raw event also, this could be useful for handling unknown events.
	// Skipping the whole dispatch pipeline when no raw handlers are
	// registered roughly halves the per-event dispatch work.
	if s.hasHandlers(eventEventType) {
		// Fast JSON decoders (goccy/sonic) may leave RawData aliasing the
		// reused zbuf; raw handlers keep the *Event past the next
		// message, so give it its own copy once on the cold raw path.
		e.RawData = append(json.RawMessage(nil), e.RawData...)
		s.handleEvent(eventEventType, e)
	}

	return e, nil
}

// ------------------------------------------------------------------------------------------------
// Code related to voice connections that initiate over the data websocket
// ------------------------------------------------------------------------------------------------

type voiceChannelJoinData struct {
	GuildID   *string `json:"guild_id"`
	ChannelID *string `json:"channel_id"`
	SelfMute  bool    `json:"self_mute"`
	SelfDeaf  bool    `json:"self_deaf"`
}

type voiceChannelJoinOp struct {
	Op   int                  `json:"op"`
	Data voiceChannelJoinData `json:"d"`
}

// ChannelVoiceJoin joins the session user to a voice channel.
//
//	gID     : Guild ID of the channel to join.
//	cID     : Channel ID of the channel to join.
//	mute    : If true, you will be set to muted upon joining.
//	deaf    : If true, you will be set to deafened upon joining.
func (s *Session) ChannelVoiceJoin(gID, cID string, mute, deaf bool) (voice *VoiceConnection, err error) {

	s.log(LogInformational, "called")

	// Lookup and insert under one write lock: a gap would let two
	// concurrent joins (user call + reconnect) create two connections
	// for the same guild, orphaning one with live goroutines.
	s.Lock()
	voice = s.VoiceConnections[gID]
	if voice == nil {
		voice = &VoiceConnection{}
		s.VoiceConnections[gID] = voice
	}
	s.Unlock()

	voice.Lock()
	voice.GuildID = gID
	voice.ChannelID = cID
	voice.deaf = deaf
	voice.mute = mute
	voice.session = s
	voice.Unlock()

	err = s.ChannelVoiceJoinManual(gID, cID, mute, deaf)
	if err != nil {
		return
	}

	// doesn't exactly work perfect yet.. TODO
	err = voice.waitUntilConnected()
	if err != nil {
		s.log(LogWarning, "error waiting for voice to connect, %s", err)
		voice.Close()
		return
	}

	return
}

// ChannelVoiceJoinManual initiates a voice session to a voice channel, but does not complete it.
//
// This should only be used when the VoiceServerUpdate will be intercepted and used elsewhere.
//
//	gID     : Guild ID of the channel to join.
//	cID     : Channel ID of the channel to join, leave empty to disconnect.
//	mute    : If true, you will be set to muted upon joining.
//	deaf    : If true, you will be set to deafened upon joining.
func (s *Session) ChannelVoiceJoinManual(gID, cID string, mute, deaf bool) (err error) {

	s.log(LogInformational, "called")

	var channelID *string
	if cID == "" {
		channelID = nil
	} else {
		channelID = &cID
	}

	// Send the request to Discord that we want to join the voice channel
	data := voiceChannelJoinOp{4, voiceChannelJoinData{&gID, channelID, mute, deaf}}
	s.RLock()
	wsConn := s.wsConn
	s.RUnlock()
	if wsConn == nil {
		return ErrWSNotFound
	}
	s.wsMutex.Lock()
	err = wsjson.Write(context.Background(), wsConn, data)
	s.wsMutex.Unlock()
	return
}

// onVoiceStateUpdate handles Voice State Update events on the data websocket.
func (s *Session) onVoiceStateUpdate(st *VoiceStateUpdate) {

	// If we don't have a connection for the channel, don't bother
	if st.ChannelID == "" {
		return
	}

	// Check if we have a voice connection to update
	s.RLock()
	voice, exists := s.VoiceConnections[st.GuildID]
	s.RUnlock()
	if !exists {
		return
	}

	// We only care about events that are about us.
	if s.State.User.ID != st.UserID {
		return
	}

	// Store the SessionID for later use.
	voice.Lock()
	voice.UserID = st.UserID
	voice.sessionID = st.SessionID
	voice.ChannelID = st.ChannelID
	voice.Unlock()
}

// onVoiceServerUpdate handles the Voice Server Update data websocket event.
//
// This is also fired if the Guild's voice region changes while connected
// to a voice channel.  In that case, need to re-establish connection to
// the new region endpoint.
func (s *Session) onVoiceServerUpdate(st *VoiceServerUpdate) {

	s.log(LogInformational, "called")

	s.RLock()
	voice, exists := s.VoiceConnections[st.GuildID]
	s.RUnlock()

	// If no VoiceConnection exists, just skip this
	if !exists {
		return
	}

	// If currently connected to voice ws/udp, then disconnect.
	// Has no effect if not connected.
	voice.Close()

	// Store values for later use
	voice.Lock()
	voice.token = st.Token
	voice.endpoint = st.Endpoint
	voice.GuildID = st.GuildID
	voice.Unlock()

	// Open a connection to the voice server
	err := voice.open()
	if err != nil {
		s.log(LogError, "onVoiceServerUpdate voice.open, %s", err)
	}
}

type identifyOp struct {
	Op   int      `json:"op"`
	Data Identify `json:"d"`
}

// identifyContext sends the identify packet to the gateway.
func (s *Session) identifyContext(ctx context.Context) error {
	s.log(LogDebug, "called")

	// TODO: This is a temporary block of code to help
	// maintain backwards compatibility
	if !s.Compress {
		s.Identify.Compress = false
	}

	// TODO: This is a temporary block of code to help
	// maintain backwards compatibility
	if s.Token != "" && s.Identify.Token == "" {
		s.Identify.Token = s.Token
	}

	// TODO: Below block should be refactored so ShardID and ShardCount
	// can be deprecated and their usage moved to the Session.Identify
	// struct
	if s.ShardCount > 1 {

		if s.ShardID >= s.ShardCount {
			return ErrWSShardBounds
		}

		s.Identify.Shard = &[2]int{s.ShardID, s.ShardCount}
	}

	// Send Identify packet to Discord
	op := identifyOp{2, s.Identify}
	s.log(LogDebug, "Identify Packet: \n%#v", op)
	s.wsMutex.Lock()
	err := wsjson.Write(ctx, s.wsConn, op)
	s.wsMutex.Unlock()

	return err
}

func (s *Session) reconnect() {

	s.log(LogInformational, "called")

	var err error

	if s.ShouldReconnectOnError {

		wait := time.Duration(1)

		for {
			s.log(LogInformational, "trying to reconnect to gateway")

			err = s.Open()
			if err == nil {
				s.log(LogInformational, "successfully reconnected to gateway")

				// I'm not sure if this is actually needed.
				// if the gw reconnect works properly, voice should stay alive
				// However, there seems to be cases where something "weird"
				// happens.  So we're doing this for now just to improve
				// stability in those edge cases.
				if s.ShouldReconnectVoiceOnSessionError {
					s.RLock()
					defer s.RUnlock()
					for _, v := range s.VoiceConnections {

						s.log(LogInformational, "reconnecting voice connection to guild %s", v.GuildID)
						go v.reconnect()

						// This is here just to prevent violently spamming the
						// voice reconnects
						time.Sleep(1 * time.Second)
					}
				}
				return
			}

			// Certain race conditions can call reconnect() twice. If this happens, we
			// just break out of the reconnect loop
			if err == ErrWSAlreadyOpen {
				s.log(LogInformational, "Websocket already exists, no need to reconnect")
				return
			}

			s.log(LogError, "error reconnecting to gateway, %s", err)

			<-time.After(wait * time.Second)
			wait *= 2
			if wait > 600 {
				wait = 600
			}
		}
	}
}

// Close closes a websocket and stops all listening/heartbeat goroutines.
// TODO: Add support for Voice WS/UDP
func (s *Session) Close() error {
	return s.CloseWithCode(int(websocket.StatusNormalClosure))
}

// CloseWithCode closes a websocket using the provided closeCode and stops all
// listening/heartbeat goroutines.
// TODO: Add support for Voice WS/UDP connections
func (s *Session) CloseWithCode(closeCode int) (err error) {

	s.log(LogInformational, "called")
	s.Lock()

	wasConnected := s.wsConn != nil || s.listening != nil || s.DataReady
	s.stopDispatchWorkers()
	s.zMu.Lock()
	if s.zr != nil {
		_ = s.zr.Close()
		s.zr = nil
	}
	s.zMu.Unlock()
	s.DataReady = false

	if s.listening != nil {
		s.log(LogInformational, "closing listening channel")
		close(s.listening)
		s.listening = nil
	}

	// TODO: Close all active Voice Connections too
	// this should force stop any reconnecting voice channels too

	if s.wsConn != nil {

		s.log(LogInformational, "sending close frame")
		// Close sends the close frame and waits (up to 5 seconds) for the
		// server to acknowledge it before closing the connection.
		s.wsMutex.Lock()
		closeErr := s.wsConn.Close(websocket.StatusCode(closeCode), "")
		s.wsMutex.Unlock()
		if closeErr != nil {
			s.log(LogInformational, "error closing websocket, %s", closeErr)
		}

		s.log(LogInformational, "closing gateway websocket")
		s.wsConn = nil
	}

	s.Unlock()

	if wasConnected {
		s.log(LogInformational, "emit disconnect event")
		s.handleEvent(disconnectEventType, &Disconnect{})
	}

	return
}
