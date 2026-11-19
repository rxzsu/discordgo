package discordgo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// stage selects where to stall: hello, ready, or no stall.
func testGateway(t *testing.T, stage string) (string, <-chan struct{}, *atomic.Int32) {
	t.Helper()
	reached := make(chan struct{})
	var once sync.Once
	var identifies atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		if stage == "hello" {
			once.Do(func() { close(reached) })
			_, _, _ = c.Read(r.Context())
			return
		}
		if err := wsjson.Write(r.Context(), c, map[string]interface{}{
			"op": 10, "d": map[string]int{"heartbeat_interval": 3600000},
		}); err != nil {
			return
		}
		var identify struct {
			Op int `json:"op"`
		}
		if err := wsjson.Read(r.Context(), c, &identify); err != nil {
			return
		}
		if identify.Op != 2 {
			t.Errorf("expected IDENTIFY, got op %d", identify.Op)
			return
		}
		identifies.Add(1)
		if stage == "ready" {
			once.Do(func() { close(reached) })
			_, _, _ = c.Read(r.Context())
			return
		}
		if err := wsjson.Write(r.Context(), c, map[string]interface{}{
			"op": 0, "t": "READY", "s": 1,
			"d": map[string]interface{}{
				"user":   map[string]string{"id": "1", "username": "test"},
				"guilds": []interface{}{}, "session_id": "test-session",
			},
		}); err != nil {
			return
		}
		once.Do(func() { close(reached) })
		// Keep reading so client close handshakes and status updates work.
		for {
			if _, _, err := c.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), reached, &identifies
}

func waitStartupResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not finish")
		return nil
	}
}

func waitStartupStage(t *testing.T, reached <-chan struct{}) {
	t.Helper()
	select {
	case <-reached:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not reach expected stage")
	}
}

func TestSessionOpenContextCancelsHandshake(t *testing.T) {
	for _, stage := range []string{"hello", "ready"} {
		t.Run(stage, func(t *testing.T) {
			gateway, reached, _ := testGateway(t, stage)
			s, _ := New("Bot test")
			s.gateway = gateway
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- s.OpenContext(ctx) }()
			waitStartupStage(t, reached)
			cancel()
			if err := waitStartupResult(t, result); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			if s.wsConn != nil {
				t.Fatal("cancelled handshake left websocket open")
			}
		})
	}
}

func TestShardManagerCloseCancelsDiscovery(t *testing.T) {
	entered := make(chan struct{})
	m := NewShardManager(ShardingConfig{Token: "raw-token"})
	m.Config.ConfigureSession = func(s *Session) {
		// This callback must be allowed to call manager methods.
		remove := m.AddHandler(func(*Session, *Ready) {})
		remove()
		s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bot raw-token" {
				t.Errorf("raw bot token was not normalized")
			}
			close(entered)
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
	}
	result := make(chan error, 1)
	go func() { result <- m.Open() }()
	waitStartupStage(t, entered)
	if err := m.Open(); !errors.Is(err, ErrWSAlreadyOpen) {
		t.Fatalf("concurrent Open: got %v", err)
	}
	m.Close()
	if err := waitStartupResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close did not cancel discovery: %v", err)
	}
}

func TestShardManagerCancelIdentifyDelay(t *testing.T) {
	for _, closeManager := range []bool{false, true} {
		name := "context"
		if closeManager {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			gateway, _, identifies := testGateway(t, "")
			m := NewShardManager(ShardingConfig{
				Token: "Bot test", ShardCount: 2, IdentifyDelay: time.Minute,
				ConfigureSession: func(s *Session) { s.gateway = gateway },
			})
			connected := make(chan struct{})
			m.AddHandlerOnce(func(*Session, *Connect) { close(connected) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- m.OpenContext(ctx) }()
			waitStartupStage(t, connected)
			if closeManager {
				m.Close()
			} else {
				cancel()
			}
			if err := waitStartupResult(t, result); !errors.Is(err, context.Canceled) {
				t.Fatalf("expected cancellation, got %v", err)
			}
			if identifies.Load() != 1 || len(m.Sessions) != 0 {
				t.Fatalf("cancellation started another shard or leaked sessions")
			}
		})
	}
}

func TestShardManagerOpenContextSuccessAndReopen(t *testing.T) {
	gateway, _, identifies := testGateway(t, "")
	m := NewShardManager(ShardingConfig{Token: "Bot test"})
	configured := 0
	m.Config.ConfigureSession = func(s *Session) {
		configured++
		s.gateway = gateway
		s.Identify.Intents = IntentsGuilds
		s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
				`{"shards":1,"session_start_limit":{"max_concurrency":1}}`))}, nil
		})}
	}
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		err := m.OpenContext(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Sessions) != 1 || m.Sessions[0].Identify.Intents != IntentsGuilds {
			t.Fatal("session configuration was not preserved")
		}
		// Cancelling the startup context must not close a successful session.
		if err := m.Sessions[0].UpdateGameStatus(0, "test"); err != nil {
			t.Fatalf("startup context controlled established connection: %v", err)
		}
		m.Close()
	}
	if configured != 4 || identifies.Load() != 2 {
		t.Fatalf("configured=%d identifies=%d", configured, identifies.Load())
	}
}

func TestShardManagerUsesIdentifyConcurrencyAndBudget(t *testing.T) {
	gateway, _, identifies := testGateway(t, "")
	remaining := 1
	m := NewShardManager(ShardingConfig{
		Token: "Bot test",
		ConfigureSession: func(s *Session) {
			s.gateway = gateway
			s.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{"shards":2,"session_start_limit":{"max_concurrency":2,"total":100,"remaining":%d,"reset_after":3600000}}`, remaining)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
		},
	})
	t.Cleanup(m.Close)
	if err := m.Open(); !errors.Is(err, ErrGatewayIdentifyBudget) {
		t.Fatalf("expected budget error, got %v", err)
	}
	if identifies.Load() != 0 {
		t.Fatal("sent IDENTIFY after failed budget check")
	}
	remaining = 2
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.OpenContext(ctx); err != nil {
		t.Fatal(err)
	}
	if identifies.Load() != 2 || len(m.Sessions) != 2 || m.Sessions[1].ShardID != 1 {
		t.Fatalf("expected both shards in ID order, got %d identifies and %d sessions", identifies.Load(), len(m.Sessions))
	}
}

func TestShardIdentifySchedulerPacesBucketsAndRefreshesBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limits := SessionInformation{Total: 3, Remaining: 3, ResetAfter: 20000, MaxConcurrency: 2}
		refreshes := 0
		scheduler := newShardIdentifyScheduler(2, DefaultIdentifyDelay, limits, func(context.Context) (*GatewayBotResponse, error) {
			refreshes++
			return &GatewayBotResponse{SessionStartLimit: limits}, nil
		})
		defer scheduler.stop()
		send := func(context.Context) error { return nil }
		ctx := context.Background()
		start := time.Now()
		for _, shardID := range []int{0, 1} {
			if err := scheduler.write(ctx, shardID, send); err != nil {
				t.Fatal(err)
			}
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("independent buckets waited %v", elapsed)
		}
		if err := scheduler.write(ctx, 2, send); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != DefaultIdentifyDelay {
			t.Fatalf("same bucket waited %v", elapsed)
		}
		if err := scheduler.write(ctx, 3, send); !errors.Is(err, ErrGatewayIdentifyBudget) {
			t.Fatalf("expected budget error, got %v", err)
		}
		time.Sleep(15 * time.Second)
		if err := scheduler.write(ctx, 3, send); err != nil || refreshes != 1 {
			t.Fatalf("budget refresh count=%d, error=%v", refreshes, err)
		}
	})
}

func TestShardManagerResumesThenIdentifiesWithinBudget(t *testing.T) {
	type attempt struct {
		packet resumePacket
		err    error
	}
	attempts := make(chan attempt, 8)
	var connections atomic.Int32
	gateway := scriptedShardGateway(t, func(ctx context.Context, c *websocket.Conn, url string) {
		connection := connections.Add(1)
		if err := wsjson.Write(ctx, c, map[string]any{"op": 10, "d": map[string]int{"heartbeat_interval": 3600000}}); err != nil {
			return
		}
		var packet resumePacket
		err := wsjson.Read(ctx, c, &packet)
		attempts <- attempt{packet, err}
		if err != nil {
			return
		}
		switch connection {
		case 1:
			writeShardReady(ctx, c, url)
			var heartbeat heartbeatOp
			if err := wsjson.Read(ctx, c, &heartbeat); err != nil || heartbeat.Op != 1 {
				return
			}
			time.Sleep(30 * time.Millisecond)
			_ = wsjson.Write(ctx, c, map[string]any{"op": 11, "d": nil})
			_ = wsjson.Write(ctx, c, map[string]any{"op": 7, "d": nil})
		case 2:
			_ = wsjson.Write(ctx, c, map[string]any{"op": 0, "t": "REPLAYED_EVENT", "s": 2, "d": map[string]any{}})
			_ = wsjson.Write(ctx, c, map[string]any{"op": 0, "t": "RESUMED", "s": 3, "d": map[string]any{}})
			_ = wsjson.Write(ctx, c, map[string]any{"op": 7, "d": nil})
		case 3:
			// Reject RESUME during the handshake, before a RESUMED event.
			_ = wsjson.Write(ctx, c, map[string]any{"op": 9, "d": false})
		case 4:
			writeShardReady(ctx, c, url)
			// A timed-out session must also be replaced without sending RESUME.
			_ = c.Close(websocket.StatusCode(4009), "session timed out")
			return
		}
		_, _, _ = c.Read(ctx)
	})
	m := testShardReconnectManager(gateway, 2)
	m.Config.IdentifyDelay = 1500 * time.Millisecond
	t.Cleanup(m.Close)
	resumed := make(chan struct{})
	m.AddHandlerOnce(func(*Session, *Resumed) { close(resumed) })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := m.OpenContext(ctx); err != nil {
		t.Fatal(err)
	}
	for index, want := range []int{2, 6, 6, 2, 0} {
		select {
		case got := <-attempts:
			if want == 0 {
				if got.err == nil {
					t.Fatalf("sent opcode %d after exhausting the identify budget", got.packet.Op)
				}
				continue
			}
			if got.err != nil || got.packet.Op != want {
				t.Fatalf("expected opcode %d, got %d (%v)", want, got.packet.Op, got.err)
			}
			if want == 6 && (got.packet.Data.SessionID != "scripted" || got.packet.Data.Sequence != int64(2*index-1)) {
				t.Fatalf("unexpected RESUME state: %+v", got.packet.Data)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for opcode %d: %v", want, ctx.Err())
		}
	}
	waitStartupStage(t, resumed)
	metrics := m.GatewayMetrics()
	if len(metrics) != 1 {
		t.Fatalf("expected metrics for one shard, got %d", len(metrics))
	}
	snapshot := metrics[0]
	if snapshot.ShardID != 0 || snapshot.ReconnectAttempts != 4 || snapshot.ReconnectSuccesses != 2 ||
		snapshot.ResumeAttempts != 2 || snapshot.ResumeSuccesses != 1 || snapshot.IdentifyBudgetRejections != 1 {
		t.Fatalf("unexpected gateway counters: %+v", snapshot)
	}
	if snapshot.HeartbeatLatency <= 0 || snapshot.IdentifyWait <= 0 {
		t.Fatalf("expected heartbeat latency and identify wait: %+v", snapshot)
	}
}

func TestShardManagerCloseCancelsReconnectHandshake(t *testing.T) {
	reconnecting := make(chan struct{})
	disconnected := make(chan struct{})
	retried := make(chan struct{}, 1)
	var connections atomic.Int32
	gateway := scriptedShardGateway(t, func(ctx context.Context, c *websocket.Conn, url string) {
		switch connections.Add(1) {
		case 1:
			_ = wsjson.Write(ctx, c, map[string]any{"op": 10, "d": map[string]int{"heartbeat_interval": 3600000}})
			var packet resumePacket
			if wsjson.Read(ctx, c, &packet) != nil {
				return
			}
			writeShardReady(ctx, c, url)
			_ = wsjson.Write(ctx, c, map[string]any{"op": 7, "d": nil})
			_, _, _ = c.Read(ctx)
		case 2:
			close(reconnecting)
			// Withhold HELLO so Close must cancel an in-flight handshake.
			_, _, _ = c.Read(ctx)
			close(disconnected)
		default:
			select {
			case retried <- struct{}{}:
			default:
			}
		}
	})
	m := testShardReconnectManager(gateway, 2)
	t.Cleanup(m.Close)
	if err := m.Open(); err != nil {
		t.Fatal(err)
	}
	waitStartupStage(t, reconnecting)
	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()
	waitStartupStage(t, closed)
	waitStartupStage(t, disconnected)
	if metrics := m.GatewayMetrics(); len(metrics) != 0 {
		t.Fatalf("manager retained metrics for closed shards: %+v", metrics)
	}
	select {
	case <-retried:
		t.Fatal("reconnected after manager Close")
	case <-time.After(1200 * time.Millisecond):
	}
}

func TestInvalidGatewaySessionPreservesOnlyResumableState(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		wantErr bool
		keep    bool
	}{
		{"resumable", "true", false, true},
		{"non-resumable", "false", false, false},
		{"malformed", `"false"`, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, err := New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			s.sessionID = "session"
			s.resumeGatewayURL = "wss://resume.example"
			atomic.StoreInt64(s.sequence, 42)

			s.Lock()
			err = s.invalidateGatewaySessionLocked([]byte(test.payload))
			s.Unlock()
			if (err != nil) != test.wantErr {
				t.Fatalf("unexpected invalid-session error: %v", err)
			}
			wantID, wantURL, wantSequence := "", "", int64(0)
			if test.keep {
				wantID, wantURL, wantSequence = "session", "wss://resume.example", 42
			}
			if s.sessionID != wantID || s.resumeGatewayURL != wantURL || atomic.LoadInt64(s.sequence) != wantSequence {
				t.Fatalf("unexpected session state: id=%q url=%q sequence=%d", s.sessionID, s.resumeGatewayURL, atomic.LoadInt64(s.sequence))
			}
		})
	}
}

func TestGatewayMetricsCountsResumedOncePerAttempt(t *testing.T) {
	s, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	packet := []byte(`{"op":0,"t":"RESUMED","s":1,"d":{}}`)
	if _, err := s.onEvent(int(websocket.MessageText), packet); err != nil {
		t.Fatal(err)
	}
	if count := s.GatewayMetrics().ResumeSuccesses; count != 0 {
		t.Fatalf("counted RESUMED without a pending attempt: %d", count)
	}
	s.gatewayMetrics.resumePending.Store(true)
	for range 2 {
		if _, err := s.onEvent(int(websocket.MessageText), packet); err != nil {
			t.Fatal(err)
		}
	}
	if count := s.GatewayMetrics().ResumeSuccesses; count != 1 {
		t.Fatalf("expected one success for the pending attempt, got %d", count)
	}
}

func testShardReconnectManager(gateway string, remaining int) *ShardManager {
	return NewShardManager(ShardingConfig{
		Token: "Bot test", IdentifyDelay: time.Millisecond,
		ConfigureSession: func(s *Session) {
			s.gateway = gateway
			s.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				body := fmt.Sprintf(`{"shards":1,"session_start_limit":{"max_concurrency":1,"total":100,"remaining":%d,"reset_after":3600000}}`, remaining)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
		},
	})
}

func scriptedShardGateway(t *testing.T, script func(context.Context, *websocket.Conn, string)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		script(ctx, c, "ws://"+r.Host)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func writeShardReady(ctx context.Context, c *websocket.Conn, url string) {
	_ = wsjson.Write(ctx, c, map[string]any{
		"op": 0, "t": "READY", "s": 1,
		"d": map[string]any{
			"user":   map[string]string{"id": "1", "username": "test"},
			"guilds": []any{}, "session_id": "scripted", "resume_gateway_url": url,
		},
	})
}

func TestShardManagerFailureClosesEarlierShards(t *testing.T) {
	gateway, _, _ := testGateway(t, "")
	m := NewShardManager(ShardingConfig{
		Token: "Bot test", ShardCount: 2, IdentifyDelay: time.Millisecond,
		ConfigureSession: func(s *Session) {
			s.gateway = gateway
			if s.ShardID == 1 {
				s.gateway = "://invalid"
			}
		},
	})
	m.AddHandler(func(*Session, *Ready) {})
	if err := m.Open(); err == nil {
		t.Fatal("expected second shard to fail")
	}
	if len(m.Sessions) != 0 || len(m.handlers[0].removers) != 0 {
		t.Fatal("failed startup retained sessions or handlers")
	}
}

func TestOpenContextAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, _ := New("Bot test")
	if err := s.OpenContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("session: %v", err)
	}
	m := NewShardManager(ShardingConfig{})
	if err := m.OpenContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("manager: %v", err)
	}
}

func TestHeartbeatDoesNotStartAfterClose(t *testing.T) {
	s, _ := New("Bot test")
	listening := make(chan interface{})
	close(listening)
	// A zero-value connection cannot be used for I/O. A stopped worker
	// must return before attempting a heartbeat or initiating a reconnect.
	s.heartbeat(&websocket.Conn{}, listening, 60000)
	if s.DataReady {
		t.Fatal("stopped heartbeat marked the session ready")
	}
}

func TestShardManagerRejectsNegativeConfiguration(t *testing.T) {
	for _, config := range []ShardingConfig{
		{ShardCount: -1},
		{IdentifyDelay: -time.Second},
	} {
		config.ConfigureSession = func(*Session) { t.Fatal("invalid configuration reached session creation") }
		m := NewShardManager(config)
		if err := m.Open(); err == nil {
			t.Fatal("expected invalid configuration error")
		}
	}
}

func TestShardLookupDuringPartialStartup(t *testing.T) {
	m := NewShardManager(ShardingConfig{ShardCount: 4})
	m.Sessions = []*Session{{ShardID: 0, ShardCount: 4}, {ShardID: 1, ShardCount: 4}}
	if got := m.ShardForGuildID("4194304"); got != m.Sessions[1] {
		t.Fatal("guild assigned to connected shard 1 was routed incorrectly")
	}
	if got := m.ShardForGuildID("8388608"); got != nil {
		t.Fatal("guild assigned to unopened shard 2 was routed to another shard")
	}
}

func TestShardManagerDisconnectHandlerCanUseManager(t *testing.T) {
	m := NewShardManager(ShardingConfig{})
	s, _ := New("Bot test")
	s.SyncEvents = true
	s.DataReady = true
	m.mu.Lock()
	m.addSessionLocked(s)
	m.mu.Unlock()
	called := make(chan struct{})
	m.AddHandler(func(*Session, *Disconnect) {
		m.ShardForGuildID("1")
		m.Close()
		close(called)
	})
	done := make(chan error, 1)
	go func() { m.Close(); done <- nil }()
	waitStartupStage(t, called)
	waitStartupResult(t, done)
}
