package discordgo

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newVoiceGatewayServer starts a discard-everything gateway websocket so
// voice code can write OP4 payloads without touching the network.
func newVoiceGatewayServer(t *testing.T) (*Session, *websocket.Conn) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)

	conn, _, err := websocket.Dial(context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	s, _ := New("")
	s.wsMutex.Lock()
	s.wsConn = conn
	s.wsMutex.Unlock()
	return s, conn
}

// ChangeChannel used to update ChannelID/deaf/mute/speaking without the
// voice lock, racing with opusSender (v.speaking) and reconnect
// (v.ChannelID/v.mute/v.deaf) which read them under the lock.
func TestVoiceChangeChannelStateUnderLock(t *testing.T) {
	s, _ := newVoiceGatewayServer(t)
	v := &VoiceConnection{session: s, GuildID: "g1", ChannelID: "c0"}

	var wg sync.WaitGroup
	wg.Add(2)
	stop := make(chan struct{})
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if err := v.ChangeChannel("c1", false, false); err != nil {
				t.Errorf("ChangeChannel: %v", err)
				return
			}
		}
		close(stop)
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			v.RLock()
			_ = v.speaking
			_ = v.ChannelID
			_ = v.mute
			_ = v.deaf
			v.RUnlock()
		}
	}()
	wg.Wait()

	v.RLock()
	got := v.ChannelID
	v.RUnlock()
	if got != "c1" {
		t.Fatalf("ChannelID = %q, want c1", got)
	}
}

// Disconnect used to write on a nil gateway connection when the session
// websocket was already gone (Close/reconnect nils it): wsjson.Write
// dereferenced the nil conn and panicked.
func TestVoiceDisconnectNilGatewayNoPanic(t *testing.T) {
	s, _ := New("")
	s.VoiceConnections = map[string]*VoiceConnection{}
	v := &VoiceConnection{session: s, GuildID: "g1", sessionID: "sid"}
	s.VoiceConnections["g1"] = v

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Disconnect panicked on nil gateway: %v", r)
		}
	}()
	if err := v.Disconnect(); err != nil {
		t.Logf("Disconnect error (expected): %v", err)
	}

	s.RLock()
	_, exists := s.VoiceConnections["g1"]
	s.RUnlock()
	if exists {
		t.Fatal("Disconnect left the connection registered")
	}
	if v.sessionID != "" {
		t.Fatalf("sessionID = %q, want empty", v.sessionID)
	}
}

// opusReceiver used to read v.aead without the lock while the OP4 event
// handler sets it under v.Lock (the same class of race already fixed in
// opusSender).
func TestVoiceOpusReceiverAeadRace(t *testing.T) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer conn.Close()

	v := &VoiceConnection{}
	closeCh := make(chan struct{})
	defer close(closeCh)

	done := make(chan struct{})
	go func() {
		defer close(done)
		v.opusReceiver(conn, closeCh, nil)
	}()

	dst, err := net.ResolveUDPAddr("udp", conn.LocalAddr().String())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	sender, err := net.DialUDP("udp", nil, dst)
	if err != nil {
		t.Fatalf("dial udp: %v", err)
	}
	defer sender.Close()

	key := make([]byte, 32)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, 12)

	// Toggle the AEAD under the lock while the receiver decrypts.
	toggleStop := make(chan struct{})
	var toggleWG sync.WaitGroup
	toggleWG.Add(1)
	go func() {
		defer toggleWG.Done()
		for {
			select {
			case <-toggleStop:
				return
			default:
			}
			v.Lock()
			v.aead = aead
			v.Unlock()
			v.Lock()
			v.aead = nil
			v.Unlock()
		}
	}()

	header := make([]byte, 12)
	header[0] = 0x80
	header[1] = 0x78
	binary.BigEndian.PutUint32(header[8:], 1)
	for i := 0; i < 20; i++ {
		binary.BigEndian.PutUint16(header[2:], uint16(i))
		payload := aead.Seal(nil, nonce, []byte("opus"), header)
		packet := append(append([]byte{}, header...), payload...)
		packet = append(packet, nonce[:4]...)
		if _, err := sender.Write(packet); err != nil {
			t.Fatalf("udp write: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(toggleStop)
	toggleWG.Wait()

	// Closing the socket unblocks the receiver; v.udpConn is unset so it
	// must not spawn a reconnect.
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("opusReceiver did not exit after socket close")
	}
}

// onEvent OP2 used to read v.wsConn/v.close without the lock while
// Close() nils/closes them concurrently.
func TestVoiceOnEventOp2CloseRace(t *testing.T) {
	s, _ := New("")
	v := &VoiceConnection{session: s, endpoint: "voice.example"}
	msg := []byte(`{"op":2,"d":{"ssrc":1,"port":1234,"modes":["aead_aes256_gcm_rtpsize"],"heartbeat_interval":5000,"ip":"127.0.0.1"}}`)

	stop := make(chan struct{})
	var closerWG sync.WaitGroup
	closerWG.Add(1)
	go func() {
		defer closerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			v.Close()
		}
	}()
	for i := 0; i < 50; i++ {
		v.onEvent(msg)
	}
	close(stop)
	closerWG.Wait()
}
