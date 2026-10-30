// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// This file contains a shard manager: it owns a set of Sessions, one per
// gateway shard, and fans event handlers out to all of them. Discord
// requires bots present on more than 2500 guilds to shard; even below
// that, sharding spreads the event load across several connections.

package discordgo

import (
	"math"
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

	// IdentifyDelay is the pause between two shard connections. When 0,
	// DefaultIdentifyDelay divided by the gateway's reported max
	// concurrency is used.
	IdentifyDelay time.Duration
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

	mu       sync.Mutex
	handlers []*shardHandlerEntry
	once     []*shardHandlerEntry
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
// Shards are connected sequentially, delaying by the identify delay
// between connections to respect Discord's identify rate limits.
func (m *ShardManager) Open() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.Sessions) > 0 {
		return ErrWSAlreadyOpen
	}

	count := m.Config.ShardCount
	delay := m.Config.IdentifyDelay

	if count == 0 || delay == 0 {
		probe, err := New(m.Config.Token)
		if err != nil {
			return err
		}
		gw, err := probe.GatewayBot()
		if err != nil {
			return err
		}
		if count == 0 {
			count = gw.Shards
			if count < 1 {
				count = 1
			}
		}
		if delay == 0 {
			delay = identifyDelayForConcurrency(gw.SessionStartLimit.MaxConcurrency)
		}
	}

	for i := 0; i < count; i++ {
		s, err := New(m.Config.Token)
		if err != nil {
			m.closeLocked()
			return err
		}
		s.ShardID = i
		s.ShardCount = count

		m.addSessionLocked(s)

		if err := s.Open(); err != nil {
			m.closeLocked()
			return err
		}

		if i < count-1 {
			// Release the manager lock while waiting so Close and
			// AddHandler stay responsive during startup.
			m.mu.Unlock()
			time.Sleep(delay)
			m.mu.Lock()
			// Close may have run while the lock was released.
			if len(m.Sessions) == 0 {
				return nil
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

// identifyDelayForConcurrency returns the delay between identify packets
// for the given max concurrency: Discord allows `concurrency` identifies
// per 5 second window.
func identifyDelayForConcurrency(concurrency int) time.Duration {
	if concurrency < 1 {
		concurrency = 1
	}
	return DefaultIdentifyDelay / time.Duration(concurrency)
}

// Close disconnects every shard. Sessions that fail to close are logged
// through the session logger; Close always disconnects all shards.
func (m *ShardManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked()
}

func (m *ShardManager) closeLocked() {
	for _, s := range m.Sessions {
		if err := s.Close(); err != nil {
			s.log(LogError, "error closing shard %d, %s", s.ShardID, err)
		}
	}
	m.Sessions = nil
}

// ShardForGuildID returns the Session responsible for the given guild.
// It returns nil before Open or when the manager has no sessions.
func (m *ShardManager) ShardForGuildID(guildID string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessions := m.sessionsLocked()
	shard, err := ShardIDForGuild(guildID, len(sessions))
	if err != nil {
		return nil
	}
	return sessions[shard]
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
	var n uint64
	if id == "" {
		return 0, ErrJSONUnmarshal
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return 0, ErrJSONUnmarshal
		}
		if n > math.MaxUint64/10 {
			return 0, ErrJSONUnmarshal
		}
		n = n*10 + uint64(c-'0')
	}
	return n, nil
}
