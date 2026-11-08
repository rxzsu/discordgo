package discordgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRESTErrorCode(t *testing.T) {
	r := RESTError{}
	if code := r.Code(); code != 0 {
		t.Fatalf("expected Code() = 0 for empty RESTError, got %d", code)
	}

	r = RESTError{Message: &APIErrorMessage{Code: 50013, Message: "Missing Permissions"}}
	if code := r.Code(); code != 50013 {
		t.Fatalf("expected Code() = 50013, got %d", code)
	}
}

func TestSleepCtxCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := sleepCtx(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("sleepCtx should return early on cancel, took %v", elapsed)
	}
}

func TestSleepCtxCompletes(t *testing.T) {
	if err := sleepCtx(context.Background(), 5*time.Millisecond); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestWithQuery(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.com/api?a=1", nil)
	cfg := &RequestConfig{Request: req}
	WithQuery("limit", "10")(cfg)
	WithQuery("a", "2")(cfg)

	if got := cfg.Request.URL.Query().Get("limit"); got != "10" {
		t.Fatalf("expected limit=10, got %q", got)
	}
	if got := cfg.Request.URL.Query().Get("a"); got != "2" {
		t.Fatalf("expected a=2, got %q", got)
	}
}

func TestWithContextAbortsRateLimitRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"retry_after": 10.0, "global": false}`))
	}))
	defer srv.Close()

	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // request should fail immediately, not after the 10s retry_after

	_, err = s.RequestWithBucketID("GET", srv.URL+"/foo", nil, srv.URL+"/foo", WithContext(ctx))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from rate limit retry, got %v", err)
	}
}

func TestWithTimeoutAbortsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until client hangs up
	}))
	defer srv.Close()

	s, err := New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.RequestWithBucketID("GET", srv.URL+"/foo", nil, srv.URL+"/foo", WithTimeout(50*time.Millisecond))
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestPollIntentsInDefaults(t *testing.T) {
	if IntentsAllWithoutPrivileged&IntentGuildMessagePolls == 0 {
		t.Fatal("IntentsAllWithoutPrivileged should include IntentGuildMessagePolls")
	}
	if IntentsAllWithoutPrivileged&IntentDirectMessagePolls == 0 {
		t.Fatal("IntentsAllWithoutPrivileged should include IntentDirectMessagePolls")
	}
	if IntentsAllWithoutPrivileged&IntentsAutoModerationConfiguration == 0 {
		t.Fatal("IntentsAllWithoutPrivileged should include IntentsAutoModerationConfiguration")
	}
}

func TestFailedHeartbeatAcksValue(t *testing.T) {
	// helloOp.HeartbeatInterval decodes a millisecond value into a
	// time.Duration (nanoseconds). The threshold must therefore include
	// one time.Millisecond factor: interval * 5ms == 5 real intervals.
	if FailedHeartbeatAcks != 5*time.Millisecond {
		t.Fatalf("expected FailedHeartbeatAcks = 5ms, got %v", FailedHeartbeatAcks)
	}
}

func TestPollVotersUnmarshal(t *testing.T) {
	body := []byte(`{"answer_id": 2, "users": [{"id": "123", "username": "tester"}]}`)
	answer := struct {
		AnswerID int     `json:"answer_id"`
		Users    []*User `json:"users"`
	}{}
	if err := unmarshal(body, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.AnswerID != 2 || len(answer.Users) != 1 || answer.Users[0].Username != "tester" {
		t.Fatalf("unexpected unmarshal result: %+v", answer)
	}
	if !strings.Contains(answer.Users[0].ID, "123") {
		t.Fatalf("unexpected user id: %q", answer.Users[0].ID)
	}
}

func TestLockBucketContextCancelled(t *testing.T) {
	r := NewRatelimiter()
	b := r.GetBucket("test-bucket")
	b.Remaining = 0
	b.reset = time.Now().Add(time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := r.LockBucketContext(ctx, "test-bucket")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("LockBucketContext should return early on cancel, took %v", elapsed)
	}

	// The bucket must have been released so it can be locked again.
	if !b.TryLock() {
		t.Fatal("bucket was not released after context cancellation")
	}
	b.Unlock()
}

func TestLockBucketContextProceeds(t *testing.T) {
	r := NewRatelimiter()
	b, err := r.LockBucketContext(context.Background(), "fresh-bucket")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Remaining != 0 {
		t.Fatalf("expected Remaining=0 after lock, got %d", b.Remaining)
	}
	b.Release(nil)
}

func TestRateLimiterRemoveAndPrune(t *testing.T) {
	r := NewRatelimiter()
	r.GetBucket("remove-me")
	r.RemoveBucket("remove-me")
	if b := r.GetBucketIfPresent("remove-me"); b != nil {
		t.Fatal("bucket should have been removed")
	}

	// Fill the shard well past the prune threshold with stale buckets;
	// pruning runs opportunistically once a shard reaches 512 entries.
	for i := 0; i < 12000; i++ {
		r.GetBucket("expired-" + strconv.Itoa(i))
	}
	total := r.BucketCount()
	if total >= 12000 {
		t.Fatalf("expected opportunistic pruning to cap bucket growth, got %d", total)
	}
	if r.GetBucketIfPresent("expired-11999") == nil {
		t.Fatal("the most recent bucket must survive pruning")
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	r := NewRatelimiter()
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				b := r.LockBucket(strconv.Itoa((w*31+i)%64) + "-bucket")
				_ = b.Release(nil)
			}
		}(w)
	}
	wg.Wait()
	if n := r.BucketCount(); n != 64 {
		t.Fatalf("expected 64 buckets after concurrent access, got %d", n)
	}
}

func TestShardIDForGuild(t *testing.T) {
	// Snowflake 1300000000000000000 has (id>>22) == 310058; 310058 % 4 == 2.
	cases := []struct {
		id    string
		count int
		want  int
	}{
		// (id>>22) == 2 for 2<<22+2.
		{"8388610", 4, 2},
		// (id>>22) == 309936103820 for this snowflake; ...820 % 4 == 0.
		{"1300000000000000000", 4, 0},
		{"1300000000000000000", 1, 0},
		{"0", 16, 0},
	}
	for _, c := range cases {
		got, err := ShardIDForGuild(c.id, c.count)
		if err != nil {
			t.Fatalf("ShardIDForGuild(%q, %d): %v", c.id, c.count, err)
		}
		if got != c.want {
			t.Fatalf("ShardIDForGuild(%q, %d) = %d, want %d", c.id, c.count, got, c.want)
		}
	}

	if _, err := ShardIDForGuild("123", 0); err != ErrWSShardBounds {
		t.Fatalf("expected ErrWSShardBounds for zero shard count, got %v", err)
	}
	if _, err := ShardIDForGuild("not-a-snowflake", 2); err == nil {
		t.Fatal("expected error for non-numeric guild ID")
	}
}

func TestParseSnowflakeInt64(t *testing.T) {
	n, err := ParseSnowflakeInt64("1300000000000000000")
	if err != nil || n != 1300000000000000000 {
		t.Fatalf("got (%d, %v)", n, err)
	}
	for _, bad := range []string{"", "12a3", "-5", "99999999999999999999999"} {
		if _, err := ParseSnowflakeInt64(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestIdentifyDelayForConcurrency(t *testing.T) {
	if got := identifyDelayForConcurrency(0); got != DefaultIdentifyDelay {
		t.Fatalf("concurrency 0 should behave like 1, got %v", got)
	}
	if got := identifyDelayForConcurrency(1); got != 5*time.Second {
		t.Fatalf("got %v", got)
	}
	if got := identifyDelayForConcurrency(16); got != 5*time.Second/16 {
		t.Fatalf("got %v", got)
	}
}

func TestShardManagerFanOut(t *testing.T) {
	m := NewShardManager(ShardingConfig{Token: "Bot test", ShardCount: 3})

	// Register before any session exists.
	called := 0
	m.AddHandler(func(s *Session, e *Connect) { called++ })

	s0, _ := New("Bot test")
	s1, _ := New("Bot test")
	s0.SyncEvents = true // dispatch synchronously so the test can assert
	// Attach sessions the same way Open does internally.
	m.mu.Lock()
	m.addSessionLocked(s0)
	m.addSessionLocked(s1)
	m.mu.Unlock()

	// Register after sessions exist.
	m.AddHandler(func(s *Session, e *Disconnect) {})

	for _, s := range m.Sessions {
		s.Lock()
		total := 0
		for _, hs := range s.handlers {
			total += len(hs)
		}
		s.Unlock()
		if total != 2 {
			t.Fatalf("shard %d: %d handlers registered, want 2", s.ShardID, total)
		}
	}

	// The pre-registered handler must actually fire on a session.
	s0.handleEvent(connectEventType, &Connect{})
	if called != 1 {
		t.Fatalf("handler fired %d times, want 1", called)
	}
}

// redirectHost is the httptest server host used to redirect Discord REST
// calls in tests.
var redirectHost string

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGuildBulkBan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/bulk-ban") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"banned_users":["1","2"],"failed_user_ids":["3"]}`))
	}))
	defer srv.Close()
	redirectHost = srv.Listener.Addr().String()

	s, _ := New("Bot test-token")
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = redirectHost
		// Discord paths carry the /api/vN prefix; the test mux has none.
		r.URL.Path = regexp.MustCompile("^/api/v[0-9]+").ReplaceAllString(r.URL.Path, "")
		return http.DefaultTransport.RoundTrip(r)
	})}
	resp, err := s.GuildBulkBan("guild-id", []string{"1", "2", "3"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.BannedUsers) != 2 || resp.BannedUsers[0] != "1" {
		t.Fatalf("unexpected banned users: %v", resp.BannedUsers)
	}
	if len(resp.FailedUserIDs) != 1 || resp.FailedUserIDs[0] != "3" {
		t.Fatalf("unexpected failed users: %v", resp.FailedUserIDs)
	}
}

func TestStickersWidgetSoundboardAPI(t *testing.T) {
	var createUploadContentType string
	mux := http.NewServeMux()
	mux.HandleFunc("/sticker-packs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sticker_packs":[{"id":"1","name":"pack"}]}`))
	})
	mux.HandleFunc("/guilds/g1/stickers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			_, _ = w.Write([]byte(`[{"id":"s1","name":"sticker","format_type":1}]`))
		case "POST":
			createUploadContentType = r.Header.Get("Content-Type")
			_, _ = w.Write([]byte(`{"id":"s1","name":"new"}`))
		}
	})
	mux.HandleFunc("/guilds/g1/stickers/s1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"s1","name":"edited"}`))
	})
	mux.HandleFunc("/guilds/g1/widget.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			_, _ = w.Write([]byte(`{"enabled":true,"channel_id":"c1"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"g1","name":"guild","presence_count":7}`))
	})
	mux.HandleFunc("/guilds/g1/soundboard-sounds", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"items":[{"id":"snd1","name":"sound","volume":0.5}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"snd1","name":"created"}`))
	})
	mux.HandleFunc("/channels/c1/send-soundboard-sound", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	redirectHost = srv.Listener.Addr().String()

	s, _ := New("Bot test-token")
	s.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = redirectHost
		// Discord paths carry the /api/vN prefix; the test mux has none.
		r.URL.Path = regexp.MustCompile("^/api/v[0-9]+").ReplaceAllString(r.URL.Path, "")
		return http.DefaultTransport.RoundTrip(r)
	})}

	packs, err := s.NitroStickerPacks()
	if err != nil || len(packs) != 1 || packs[0].Name != "pack" {
		t.Fatalf("NitroStickerPacks: %v %+v", err, packs)
	}

	stickers, err := s.GuildStickers("g1")
	if err != nil || len(stickers) != 1 || stickers[0].ID != "s1" {
		t.Fatalf("GuildStickers: %v %+v", err, stickers)
	}

	created, err := s.GuildStickerCreate("g1", &StickerParams{Name: "new", Tags: "tag"}, &File{Name: "s.png", Reader: strings.NewReader("pngdata")})
	if err != nil || created.Name != "new" {
		t.Fatalf("GuildStickerCreate: %v %+v", err, created)
	}
	if !strings.HasPrefix(createUploadContentType, "multipart/form-data") {
		t.Fatalf("sticker create must be multipart, got %q", createUploadContentType)
	}

	widget, err := s.GuildWidget("g1")
	if err != nil || widget.Name != "guild" || widget.PresenceCount != 7 {
		t.Fatalf("GuildWidget: %v %+v", err, widget)
	}

	enabled := true
	wEdits, err := s.GuildWidgetEdit("g1", &GuildWidgetEditParams{Enabled: &enabled})
	if err != nil || !wEdits.Enabled || wEdits.ChannelID != "c1" {
		t.Fatalf("GuildWidgetEdit: %v %+v", err, wEdits)
	}

	sounds, err := s.GuildSoundboardSounds("g1")
	if err != nil || len(sounds) != 1 || sounds[0].Volume != 0.5 {
		t.Fatalf("GuildSoundboardSounds: %v %+v", err, sounds)
	}

	if err := s.ChannelSoundboardSoundSend("c1", "snd1", ""); err != nil {
		t.Fatalf("ChannelSoundboardSoundSend: %v", err)
	}
}

func TestVoiceHeartbeatLatency(t *testing.T) {
	v := &VoiceConnection{}
	if lat := v.VoiceHeartbeatLatency(); lat != 0 {
		t.Fatalf("expected zero latency before first exchange, got %v", lat)
	}
	v.LastVoiceHeartbeatSent = time.Now().Add(-50 * time.Millisecond)
	v.LastVoiceHeartbeatAck = time.Now()
	if lat := v.VoiceHeartbeatLatency(); lat <= 0 {
		t.Fatalf("expected positive latency, got %v", lat)
	}
}

func TestShardManagerRemoveHandlerNoPanic(t *testing.T) {
	// Funcs are uncomparable: the old implementation panicked with
	// "comparing uncomparable type func(...)" on the first remove() call.
	m := NewShardManager(ShardingConfig{Token: "Bot test", ShardCount: 2})
	s0, _ := New("Bot test")
	m.mu.Lock()
	m.addSessionLocked(s0)
	m.mu.Unlock()

	called := 0
	remove := m.AddHandler(func(s *Session, e *Connect) { called++ })
	remove() // must not panic

	m.mu.Lock()
	if len(m.handlers) != 0 {
		t.Fatalf("expected 0 handlers after remove, got %d", len(m.handlers))
	}
	m.mu.Unlock()
}

func TestThreadMembersUpdateMultiRemove(t *testing.T) {
	st := NewState()
	g := &Guild{ID: "g1"}
	st.guildMap["g1"] = g
	st.Guilds = []*Guild{g}
	th := &Channel{ID: "t1", Type: ChannelTypeGuildPublicThread, GuildID: "g1"}
	th.Members = []*ThreadMember{{UserID: "u1"}, {UserID: "u2"}, {UserID: "u3"}}
	st.channelMap["t1"] = th
	g.Channels = []*Channel{th}

	err := st.ThreadMembersUpdate(&ThreadMembersUpdate{
		ID:             "t1",
		GuildID:        "g1",
		RemovedMembers: []string{"u1", "u3"}, // 2+ removals used to panic
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Members) != 1 || th.Members[0].UserID != "u2" {
		t.Fatalf("unexpected members after removal: %+v", th.Members)
	}
}

func TestGuildRemoveCleansIndexes(t *testing.T) {
	st := NewState()
	g := &Guild{ID: "g1"}
	st.guildMap["g1"] = g
	st.Guilds = []*Guild{g}
	ch := &Channel{ID: "c1", GuildID: "g1"}
	st.channelMap["c1"] = ch
	g.Channels = []*Channel{ch}
	st.memberMap["g1"] = map[string]*Member{"u1": {User: &User{ID: "u1"}}}

	if err := st.GuildRemove(&Guild{ID: "g1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Channel("c1"); err == nil {
		t.Fatal("channel of a removed guild must not be found")
	}
	if _, err := st.Member("g1", "u1"); err == nil {
		t.Fatal("member of a removed guild must not be found")
	}
	if _, err := st.Guild("g1"); err == nil {
		t.Fatal("removed guild must not be found")
	}
}

func TestOnReadyResetsIndexes(t *testing.T) {
	st := NewState()
	// Simulate a previous session with a guild that will be absent from
	// the new READY.
	old := &Guild{ID: "old"}
	st.guildMap["old"] = old
	st.Guilds = []*Guild{old}
	oldCh := &Channel{ID: "oldc", GuildID: "old"}
	st.channelMap["oldc"] = oldCh
	old.Channels = []*Channel{oldCh}

	err := st.onReady(&Session{StateEnabled: true}, &Ready{Guilds: []*Guild{{ID: "new"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Guild("old"); err == nil {
		t.Fatal("guild absent from READY must not remain in state")
	}
	if _, err := st.Channel("oldc"); err == nil {
		t.Fatal("channel absent from READY must not remain in state")
	}
	if _, err := st.Guild("new"); err != nil {
		t.Fatalf("new guild must be indexed: %v", err)
	}
}

func TestParseGatewayEnvelope(t *testing.T) {
	// Payloads the gateway actually sends: the fast scan must agree with the
	// general JSON decoder on every field, or reject the payload entirely.
	cases := []string{
		`{"t":null,"s":11,"op":11,"d":{}}`,
		`{"t":"MESSAGE_CREATE","s":1,"op":0,"d":{"id":"1","content":"hi"}}`,
		`{"op":10,"d":{"heartbeat_interval":41250}}`,
		`{"op":1}`,
		`{"s":42,"t":"READY","op":0,"d":{"user":{"id":"9"}},"session_id":"abc"}`,
		`{}`,
		` {"t" : "GUILD_CREATE" , "s" : 7 , "op" : 0 , "d" : { "id" : "g" } } `,
		`{"t":"X","s":1,"op":0,"d":null}`,
		`{"unknown":{"nested":{"deep":[1,2,{"a":"}"}]}},"t":"Y","s":2,"op":0,"d":[]}`,
		`{"t":"X","s":1,"op":0,"d":{"big":"` + strings.Repeat("x", 8192) + `"}}`,
	}
	for _, tc := range cases {
		var want *Event
		if err := Unmarshal([]byte(tc), &want); err != nil {
			t.Fatalf("reference decode failed for %q: %v", tc, err)
		}
		ge, ok := parseGatewayEnvelope([]byte(tc))
		if !ok {
			t.Fatalf("fast scan rejected valid payload %q", tc)
		}
		if ge.op != want.Operation || ge.seq != want.Sequence || ge.typ != want.Type {
			t.Fatalf("mismatch for %q: got op=%d seq=%d typ=%q, want op=%d seq=%d typ=%q",
				tc, ge.op, ge.seq, ge.typ, want.Operation, want.Sequence, want.Type)
		}
		if len(ge.data) != len(want.RawData) {
			t.Fatalf("data length mismatch for %q: got %d, want %d", tc, len(ge.data), len(want.RawData))
		}
		// RawData bytes must be identical (encoding may differ only in
		// whitespace the decoder preserved, so compare post-decode).
		var gotStruct, wantStruct map[string]interface{}
		if len(ge.data) > 0 {
			if err := json.Unmarshal(ge.data, &gotStruct); err != nil && string(ge.data) != "null" && string(ge.data) != "[]" {
				t.Fatalf("fast-scan data not decodable for %q: %v", tc, err)
			}
		}
		if len(want.RawData) > 0 {
			if err := json.Unmarshal(want.RawData, &wantStruct); err != nil && string(want.RawData) != "null" && string(want.RawData) != "[]" {
				t.Fatalf("reference data not decodable for %q: %v", tc, err)
			}
		}
		gj, _ := json.Marshal(gotStruct)
		wj, _ := json.Marshal(wantStruct)
		if string(gj) != string(wj) {
			t.Fatalf("data content mismatch for %q: got %s, want %s", tc, gj, wj)
		}
	}

	// Invalid payloads must be rejected so the general decoder reports them.
	// Also: an escaped event name is legal JSON the gateway never sends; the
	// fast scan declines it and onEvent falls back to the general decoder.
	bad := []string{
		`{"t":"esc\taped","s":1,"op":0,"d":{}}`,
		``,
		`not json`,
		`[]`,
		`{"t":"X","s":1,"op":0,"d":{}} extra`,
		`{"t":"X","s":1,"op":0,"d":{`,
		`{"t":"X","s":1,"op":0,"d":{"a":tru}}`,
		`{"t":"X","s":1,"op":0,"d":{"a":01}}`,
		`{"t":"X","s":1,"op":0,"d":{"a":1e}}`,
		`{"t":"X","s":1,"op":0,"d":"unclosed`,
		`{"t":"X","s":1,"op":0,"d":{"a":"ctrl\x01"}}`,
		`{"t":"X","s":1,"op":0`,
		`{"t":"X","s":1,"op":0,"d":{}}{"x":1}`,
		`{"t":"X","s":1,"op":0,"d":{"a":"b"x}}`,
		`{"t":"X","s":1,"op":0,"d":[1,tru]}`,
		`{"t":"X","s":1,"op":0,"d":{"a":1,"b":}}`,
		`{"t":"X","s":1,"op":0,"d":[,]}`,
		`{"t":"X","s":1,"op":0,"d":{"a" 1}}`,
		`{"t":"X","s":1,"op":0,"d":[1,2,]}`,
		`{"t":"X","s":1,"op":0,"d":{"a":{"b":[1,{"c":"}"}]},"z":tru}}`,
		`{"t":"X","s":1,"op":0,"d":truex}`,
		`{"t":"X","s":1,"op":0,"d":nullx}`,
		`{"t":"X","s":1,"op":0,"d":[1 2]}`,
		`{"t":"X","s":1,"op":0,"d":+.5}`,
		`{"t":"X","s":1,"op":0,"d":{}x}`,
	}
	for _, tc := range bad {
		if _, ok := parseGatewayEnvelope([]byte(tc)); ok {
			t.Fatalf("fast scan accepted invalid payload %q", tc)
		}
		var e *Event
		if err := Unmarshal([]byte(tc), &e); err == nil && tc != "" {
			t.Logf("note: reference decoder also accepts %q", tc)
		}
	}

	// Invariant: whatever the fast scan accepts, the reference decoder must
	// accept with identical envelope fields. Checked over truncations and
	// byte mutations of a valid payload.
	base := []byte(`{"t":"MESSAGE_CREATE","s":17,"op":0,"d":{"id":"9","content":"a[b{c}d\"e\\f","n":-1.5e2,"ok":true,"nil":null}}`)
	for i := 0; i <= len(base); i++ {
		trunc := base[:i]
		ge, ok := parseGatewayEnvelope(trunc)
		var want *Event
		err := Unmarshal(trunc, &want)
		if ok {
			if err != nil {
				t.Fatalf("fast scan accepted %q which the decoder rejects: %v", trunc, err)
			}
			if ge.op != want.Operation || ge.seq != want.Sequence || ge.typ != want.Type {
				t.Fatalf("mutation mismatch for %q", trunc)
			}
		}
	}
	for i := 0; i < len(base); i++ {
		for _, repl := range []byte{'"', 'x', '{', '[', '}', ']', ':', ',', '0', '-', ' ', '\\'} {
			mut := append(append([]byte{}, base[:i]...), repl)
			mut = append(mut, base[i+1:]...)
			ge, ok := parseGatewayEnvelope(mut)
			var want *Event
			err := Unmarshal(mut, &want)
			if ok {
				if err != nil {
					t.Fatalf("fast scan accepted %q which the decoder rejects: %v", mut, err)
				}
				if ge.op != want.Operation || ge.seq != want.Sequence || ge.typ != want.Type {
					t.Fatalf("mutation mismatch for %q", mut)
				}
			}
		}
	}
}

func TestOnEventFastPathEquivalence(t *testing.T) {
	s, _ := New("Bot test-token")
	payload := []byte(`{"t":"MESSAGE_CREATE","s":7,"op":0,"d":{"id":"42","channel_id":"c","author":{"id":"u","username":"n"},"content":"hello"}}`)
	e, err := s.onEvent(int(1), payload)
	if err != nil {
		t.Fatal(err)
	}
	if e.Operation != 0 || e.Sequence != 7 || e.Type != "MESSAGE_CREATE" {
		t.Fatalf("bad envelope: op=%d seq=%d type=%q", e.Operation, e.Sequence, e.Type)
	}
	mc, ok := e.Struct.(*MessageCreate)
	if !ok || mc.Message == nil {
		t.Fatal("typed struct not decoded")
	}
	if mc.ID != "42" || mc.ChannelID != "c" || mc.Content != "hello" || mc.Author == nil || mc.Author.ID != "u" {
		t.Fatalf("bad decoded message: %+v", mc.Message)
	}
	if e.RawData == nil || !bytes.Contains(e.RawData, []byte(`"hello"`)) {
		t.Fatalf("RawData lost: %s", e.RawData)
	}
	// GuildID is set by MessageCreate.UnmarshalJSON from the message context.
	if mc.GuildID != "" {
		t.Fatalf("unexpected GuildID: %q", mc.GuildID)
	}
}
