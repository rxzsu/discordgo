// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// This file contains a shard manager: it owns a set of Sessions, one per
// gateway shard, and fans event handlers out to all of them. Discord
// requires bots present on more than 2500 guilds to shard; even below
// that, sharding spreads the event load across several connections.

package discordgo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultIdentifyDelay is the pause between two shard IDENTIFYs when the
// bot has no large-bot sharding privileges: Discord allows one identify
// per 5 seconds per bucket.
const DefaultIdentifyDelay = 5 * time.Second

// ShardingConfig configures a ShardManager.
type ShardingConfig struct {
	// Token is the bot token, "Bot " prefix included or not.
	Token string

	// ShardCount is the total number of shards to open. When 0, the
	// manager uses the recommended count from the GatewayBot endpoint.
	ShardCount int

	// IdentifyDelay overrides automatic scheduling with sequential startup
	// and a delay between shard connections. When 0, the manager uses the
	// Gateway Bot endpoint to schedule identifies in parallel buckets.
	IdentifyDelay time.Duration

	// ConfigureSession is called before gateway discovery and before opening
	// each shard. Use it to set intents, the HTTP client, or other settings.
	// It runs without the manager lock; it must not modify ShardID/ShardCount.
	ConfigureSession func(*Session)
}

// ShardManager manages a set of gateway shards, each backed by its own
// Session. Register handlers on the manager and they fire for events
// from every shard.
type ShardManager struct {
	// Config holds the manager configuration; set it via NewShardManager.
	Config ShardingConfig

	// Sessions holds one Session per shard, indexed by shard ID, after
	// Open has been called.
	Sessions []*Session

	opening    bool
	closing    int
	openCancel context.CancelFunc
	identify   *shardIdentifyScheduler
	mu         sync.Mutex
	handlers   []*shardHandlerEntry
	once       []*shardHandlerEntry
}

// shardHandlerEntry wraps a handler so the manager can remove it by
// pointer identity. Comparing the handler values themselves would panic
// at run time for closures (funcs are uncomparable in Go).
type shardHandlerEntry struct {
	h interface{}
	// removers holds one removal function per session the handler is
	// attached to (including shards connected after registration).
	// Guarded by ShardManager.mu.
	removers map[*Session]func()
}

// NewShardManager returns a ShardManager for the given configuration.
func NewShardManager(config ShardingConfig) *ShardManager {
	return &ShardManager{Config: config}
}

// AddHandler registers an event handler on every shard, including shards
// connected later by Open. The API mirrors Session.AddHandler; the
// returned removal function detaches the handler everywhere.
func (m *ShardManager) AddHandler(handler interface{}) func() {
	entry := &shardHandlerEntry{h: handler, removers: map[*Session]func(){}}
	m.mu.Lock()
	m.handlers = append(m.handlers, entry)
	sessions := m.sessionsLocked()
	for _, s := range sessions {
		entry.removers[s] = s.AddHandler(handler)
	}
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			for i, e := range m.handlers {
				if e == entry {
					m.handlers = append(m.handlers[:i], m.handlers[i+1:]...)
					break
				}
			}
			removers := make([]func(), 0, len(entry.removers))
			for _, rm := range entry.removers {
				removers = append(removers, rm)
			}
			m.mu.Unlock()
			for _, rm := range removers {
				rm()
			}
		})
	}
}

// AddHandlerOnce is AddHandler with Session.AddHandlerOnce semantics:
// the handler fires once per shard and is then removed.
func (m *ShardManager) AddHandlerOnce(handler interface{}) func() {
	entry := &shardHandlerEntry{h: handler, removers: map[*Session]func(){}}
	m.mu.Lock()
	m.once = append(m.once, entry)
	sessions := m.sessionsLocked()
	for _, s := range sessions {
		entry.removers[s] = s.AddHandlerOnce(handler)
	}
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			for i, e := range m.once {
				if e == entry {
					m.once = append(m.once[:i], m.once[i+1:]...)
					break
				}
			}
			removers := make([]func(), 0, len(entry.removers))
			for _, rm := range entry.removers {
				removers = append(removers, rm)
			}
			m.mu.Unlock()
			for _, rm := range removers {
				rm()
			}
		})
	}
}

// sessionsLocked returns a copy of the session slice; m.mu must be held.
func (m *ShardManager) sessionsLocked() []*Session {
	out := make([]*Session, len(m.Sessions))
	copy(out, m.Sessions)
	return out
}

// Open connects all shards. When the configured ShardCount is 0, the
// recommended count is fetched from the GatewayBot endpoint first.
// By default, shards start in groups based on Discord's identify buckets.
func (m *ShardManager) Open() error {
	return m.OpenContext(context.Background())
}

// OpenContext connects all shards, respecting ctx during gateway discovery,
// connection handshakes and identify delays. Cancellation or failure closes
// any shards already started. Close also cancels an in-progress startup.
// After success ctx no longer controls the connected sessions.
func (m *ShardManager) OpenContext(ctx context.Context) (err error) {
	m.mu.Lock()
	if m.opening || m.closing > 0 || len(m.Sessions) > 0 {
		m.mu.Unlock()
		return ErrWSAlreadyOpen
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	config := m.Config
	if config.ShardCount < 0 || config.IdentifyDelay < 0 {
		m.mu.Unlock()
		return fmt.Errorf("ShardCount and IdentifyDelay must be nonnegative")
	}
	ctx, cancel := context.WithCancel(ctx)
	m.opening, m.openCancel = true, cancel
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if err == nil {
			err = ctx.Err()
		}
		cancel()
		var sessions []*Session
		if err != nil {
			sessions = m.detachSessionsLocked()
		}
		m.mu.Unlock()
		closeShardSessions(sessions)
		m.mu.Lock()
		m.opening, m.openCancel = false, nil
		m.mu.Unlock()
	}()

	count, delay := config.ShardCount, config.IdentifyDelay
	concurrency := 1
	var limits SessionInformation
	var getGateway func(context.Context) (*GatewayBotResponse, error)
	token := config.Token
	if token != "" && !strings.HasPrefix(token, "Bot ") {
		token = "Bot " + token
	}
	newSession := func(id int) (*Session, error) {
		s, err := New(token)
		if err != nil {
			return nil, err
		}
		s.ShardID, s.ShardCount = id, count
		if config.ConfigureSession != nil {
			config.ConfigureSession(s)
		}
		return s, nil
	}

	if count == 0 || delay == 0 {
		probe, err := newSession(0)
		if err != nil {
			return err
		}
		getGateway = func(ctx context.Context) (*GatewayBotResponse, error) {
			return probe.GatewayBot(WithContext(ctx))
		}
		gw, err := getGateway(ctx)
		if err != nil {
			return err
		}
		limits = gw.SessionStartLimit
		if count == 0 {
			count = gw.Shards
			if count < 1 {
				count = 1
			}
		}
		if delay == 0 {
			concurrency = min(count, max(1, limits.MaxConcurrency))
		}
	}
	if limits.Total > 0 && count > limits.Remaining {
		return fmt.Errorf("%w: need %d, have %d (resets in %s)", ErrGatewayIdentifyBudget, count, limits.Remaining, identifyResetDelay(limits))
	}
	bucketDelay := delay
	if bucketDelay == 0 {
		bucketDelay = DefaultIdentifyDelay
	}
	scheduler := newShardIdentifyScheduler(concurrency, bucketDelay, limits, getGateway)
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		scheduler.stop()
		return err
	}
	m.identify = scheduler
	m.mu.Unlock()

	for start := 0; start < count; start += concurrency {
		end := min(start+concurrency, count)
		sessions := make([]*Session, 0, end-start)
		for i := start; i < end; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			s, err := newSession(i)
			if err != nil {
				return err
			}
			s.identifyScheduler = scheduler
			sessions = append(sessions, s)
		}
		m.mu.Lock()
		err := ctx.Err()
		if err == nil {
			for _, s := range sessions {
				m.addSessionLocked(s)
			}
		}
		m.mu.Unlock()
		if err != nil {
			return err
		}
		if err := openShardGroup(ctx, sessions); err != nil {
			return err
		}
		if delay > 0 && end < count {
			if err := sleepCtx(ctx, delay); err != nil {
				return err
			}
		}
	}
	return nil
}

// addSessionLocked registers a session as a shard and attaches every
// handler registered so far; m.mu must be held.
func (m *ShardManager) addSessionLocked(s *Session) {
	for _, e := range m.handlers {
		e.removers[s] = s.AddHandler(e.h)
	}
	for _, e := range m.once {
		e.removers[s] = s.AddHandlerOnce(e.h)
	}
	m.Sessions = append(m.Sessions, s)
}

func openShardGroup(ctx context.Context, sessions []*Session) error {
	if len(sessions) == 1 {
		return sessions[0].OpenContext(ctx)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Go(func() {
			if err := s.OpenContext(ctx); err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	return context.Cause(ctx)
}

// Close disconnects every shard. Sessions that fail to close are logged
// through the session logger; Close always disconnects all shards.
func (m *ShardManager) Close() {
	m.mu.Lock()
	if m.openCancel != nil {
		m.openCancel()
	}
	m.closing++
	sessions := m.detachSessionsLocked()
	m.mu.Unlock()
	// Session.Close emits Disconnect; callbacks may call manager methods.
	closeShardSessions(sessions)
	m.mu.Lock()
	m.closing--
	m.mu.Unlock()
}

// detachSessionsLocked removes references before any user callbacks run.
func (m *ShardManager) detachSessionsLocked() []*Session {
	if m.identify != nil {
		m.identify.stop()
		m.identify = nil
	}
	sessions := m.Sessions
	m.Sessions = nil
	for _, s := range sessions {
		for _, e := range m.handlers {
			delete(e.removers, s)
		}
		for _, e := range m.once {
			delete(e.removers, s)
		}
	}
	return sessions
}

// ErrGatewayIdentifyBudget indicates that the Gateway Bot endpoint reported
// too few remaining session starts for a new IDENTIFY.
var ErrGatewayIdentifyBudget = errors.New("gateway identify budget exhausted")

var errShardIdentifyStopped = errors.New("shard identify scheduler stopped")

type shardIdentifyBucket struct {
	permit chan struct{}
	next   time.Time
}

type shardIdentifyScheduler struct {
	ctx     context.Context
	stop    context.CancelFunc
	buckets []shardIdentifyBucket
	delay   time.Duration

	mu         sync.Mutex
	limits     SessionInformation
	resetAt    time.Time
	getGateway func(context.Context) (*GatewayBotResponse, error)
}

func identifyResetDelay(limits SessionInformation) time.Duration {
	return time.Duration(limits.ResetAfter) * time.Millisecond
}

func newShardIdentifyScheduler(concurrency int, delay time.Duration, limits SessionInformation, getGateway func(context.Context) (*GatewayBotResponse, error)) *shardIdentifyScheduler {
	ctx, stop := context.WithCancel(context.Background())
	s := &shardIdentifyScheduler{
		ctx: ctx, stop: stop, buckets: make([]shardIdentifyBucket, concurrency), delay: delay,
		limits: limits, resetAt: time.Now().Add(identifyResetDelay(limits)), getGateway: getGateway,
	}
	for i := range s.buckets {
		s.buckets[i].permit = make(chan struct{}, 1)
	}
	return s
}

func (s *shardIdentifyScheduler) write(ctx context.Context, shardID int, send func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(s.ctx, func() { cancel(errShardIdentifyStopped) })
	defer stop()
	defer cancel(nil)
	if s.ctx.Err() != nil {
		return errShardIdentifyStopped
	}
	b := &s.buckets[shardID%len(s.buckets)]
	select {
	case b.permit <- struct{}{}:
		defer func() { <-b.permit }()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	if wait := time.Until(b.next); wait > 0 {
		if err := sleepCtx(ctx, wait); err != nil {
			return context.Cause(ctx)
		}
	}
	if err := s.consume(ctx); err != nil {
		return err
	}
	err := send(ctx)
	b.next = time.Now().Add(s.delay)
	return err
}

func (s *shardIdentifyScheduler) consume(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if s.limits.Total <= 0 {
		return nil
	}
	if s.limits.ResetAfter > 0 && !time.Now().Before(s.resetAt) {
		gw, err := s.getGateway(ctx)
		if err != nil {
			return err
		}
		if gw.SessionStartLimit.Total <= 0 {
			return ErrGatewayIdentifyBudget
		}
		s.limits = gw.SessionStartLimit
		s.resetAt = time.Now().Add(identifyResetDelay(s.limits))
	}
	if s.limits.Remaining <= 0 {
		return ErrGatewayIdentifyBudget
	}
	s.limits.Remaining--
	return nil
}

func closeShardSessions(sessions []*Session) {
	for _, s := range sessions {
		if err := s.Close(); err != nil {
			s.log(LogError, "error closing shard %d, %s", s.ShardID, err)
		}
	}
}

// ShardForGuildID returns the Session responsible for the given guild.
// It returns nil before Open, after Close, or if that shard has not been
// created yet during startup.
func (m *ShardManager) ShardForGuildID(guildID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Sessions) == 0 {
		return nil
	}
	count := m.Sessions[0].ShardCount
	if count < 1 {
		count = len(m.Sessions)
	}
	shard, err := ShardIDForGuild(guildID, count)
	if err != nil || shard >= len(m.Sessions) {
		return nil
	}
	return m.Sessions[shard]
}

// ShardIDForGuild calculates the shard ID a guild is assigned to for the
// given shard count: (snowflake >> 22) % shardCount.
func ShardIDForGuild(guildID string, shardCount int) (int, error) {
	if shardCount < 1 {
		return 0, ErrWSShardBounds
	}
	id, err := ParseSnowflakeInt64(guildID)
	if err != nil {
		return 0, err
	}
	return int((id >> 22) % uint64(shardCount)), nil
}

// ParseSnowflakeInt64 parses a Discord snowflake ID into a uint64.
func ParseSnowflakeInt64(id string) (uint64, error) {
	// Reject signs as well as out-of-range decimal values.
	if id == "" || id[0] < '0' || id[0] > '9' {
		return 0, ErrJSONUnmarshal
	}
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0, ErrJSONUnmarshal
	}
	return n, nil
}
