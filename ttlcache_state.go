// Discordgo - Discord bindings for Go
// Available at https://github.com/rxzsu/discordgo

// Copyright 2015-2016 Bruce Marriner <bruce@sqls.net>.  All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This file provides CachedSession, an optional integration wrapper that
// layers the generic TTLCache (ttlcache.go) on top of a Discordgo session:
// frequently re-read REST results such as channels and guild members are
// cached for a short time instead of being fetched on every call. The
// wrapper only uses public Session methods and public event handlers;
// state.go is not modified by any of this.

package discordgo

import (
	"context"
	"sync"
	"time"
)

// Defaults for CachedSessionOptions.
const (
	// DefaultCachedSessionChannelTTL is the default channel entry TTL.
	DefaultCachedSessionChannelTTL = 30 * time.Second
	// DefaultCachedSessionMemberTTL is the default member entry TTL.
	DefaultCachedSessionMemberTTL = 60 * time.Second
	// DefaultCachedSessionMaxEntries caps both channel and member caches.
	DefaultCachedSessionMaxEntries = 4096
)

// CachedSessionOptions configures a CachedSession.
type CachedSessionOptions struct {
	// ChannelTTL is how long a fetched channel stays cached. Zero means
	// DefaultCachedSessionChannelTTL; negative values disable the channel
	// cache entirely.
	ChannelTTL time.Duration
	// MemberTTL is how long a fetched guild member stays cached. Zero
	// means DefaultCachedSessionMemberTTL; negative values disable the
	// member cache entirely.
	MemberTTL time.Duration
	// MaxChannels caps the channel cache; zero means
	// DefaultCachedSessionMaxEntries.
	MaxChannels int
	// MaxMembers caps the member cache; zero means
	// DefaultCachedSessionMaxEntries.
	MaxMembers int
	// Jitter is forwarded to both caches (see TTLCacheOptions.Jitter).
	Jitter time.Duration
	// JanitorInterval is forwarded to both caches.
	JanitorInterval time.Duration
	// DisableAutoInvalidate turns off the gateway-event handlers that
	// drop stale entries (ChannelUpdate, ChannelDelete, GuildMemberUpdate,
	// GuildMemberRemove, GuildDelete). Callers are then responsible for
	// calling Invalidate* themselves.
	DisableAutoInvalidate bool
}

// memberCacheKey identifies a member within a guild.
type memberCacheKey struct {
	GuildID string
	UserID  string
}

// CachedSession caches short-lived REST lookups (channels, guild members)
// over a session's public API. It complements State: State tracks events
// long-term, while CachedSession adds a small TTL layer in front of REST
// calls like Channel and GuildMember, deduplicating concurrent fetches
// with singleflight semantics. Safe for concurrent use; Close removes its
// event handlers and stops the janitors.
type CachedSession struct {
	s        *Session
	channels *TTLCache[string, *Channel]
	members  *TTLCache[memberCacheKey, *Member]

	mu        sync.Mutex
	closeOnce sync.Once
	removed   []func()
}

// NewCachedSession wraps s with TTL caches and, unless
// opts.DisableAutoInvalidate is set, subscribes to the gateway events that
// invalidate stale entries.
func NewCachedSession(s *Session, opts CachedSessionOptions) *CachedSession {
	c := &CachedSession{s: s}

	channelTTL := opts.ChannelTTL
	if channelTTL == 0 {
		channelTTL = DefaultCachedSessionChannelTTL
	}
	memberTTL := opts.MemberTTL
	if memberTTL == 0 {
		memberTTL = DefaultCachedSessionMemberTTL
	}
	maxChannels := opts.MaxChannels
	if maxChannels == 0 {
		maxChannels = DefaultCachedSessionMaxEntries
	}
	maxMembers := opts.MaxMembers
	if maxMembers == 0 {
		maxMembers = DefaultCachedSessionMaxEntries
	}

	if channelTTL > 0 {
		c.channels = NewTTLCache(TTLCacheOptions[string, *Channel]{
			TTL:             channelTTL,
			Jitter:          opts.Jitter,
			MaxEntries:      maxChannels,
			JanitorInterval: opts.JanitorInterval,
		})
	}
	if memberTTL > 0 {
		c.members = NewTTLCache(TTLCacheOptions[memberCacheKey, *Member]{
			TTL:             memberTTL,
			Jitter:          opts.Jitter,
			MaxEntries:      maxMembers,
			JanitorInterval: opts.JanitorInterval,
		})
	}

	if !opts.DisableAutoInvalidate {
		c.registerInvalidationHandlers()
	}
	return c
}

// Session returns the wrapped session.
func (c *CachedSession) Session() *Session {
	return c.s
}

// registerInvalidationHandlers subscribes to gateway events that make
// cached channels or members stale. Uses the public AddHandler API.
func (c *CachedSession) registerInvalidationHandlers() {
	if c.channels != nil {
		c.addHandler(c.s.AddHandler(func(_ *Session, e *ChannelUpdate) {
			if e != nil && e.Channel != nil {
				c.InvalidateChannel(e.ID)
			}
		}))
		c.addHandler(c.s.AddHandler(func(_ *Session, e *ChannelDelete) {
			if e != nil && e.Channel != nil {
				c.InvalidateChannel(e.ID)
			}
		}))
	}
	if c.members != nil {
		c.addHandler(c.s.AddHandler(func(_ *Session, e *GuildMemberUpdate) {
			if e != nil && e.Member != nil && e.User != nil {
				c.InvalidateMember(e.GuildID, e.User.ID)
			}
		}))
		c.addHandler(c.s.AddHandler(func(_ *Session, e *GuildMemberRemove) {
			if e != nil && e.Member != nil && e.User != nil {
				c.InvalidateMember(e.GuildID, e.User.ID)
			}
		}))
		c.addHandler(c.s.AddHandler(func(_ *Session, e *GuildDelete) {
			if e != nil && e.Guild != nil {
				c.InvalidateGuild(e.ID)
			}
		}))
	}
}

// addHandler remembers the removal function so Close can unsubscribe.
func (c *CachedSession) addHandler(remove func()) {
	c.mu.Lock()
	c.removed = append(c.removed, remove)
	c.mu.Unlock()
}

// ------------------------------------------------------------------------------------------------
// Cached lookups
// ------------------------------------------------------------------------------------------------

// Channel returns the channel by ID: from the TTL cache when fresh,
// otherwise via Session.Channel, which is then cached. ctx cancels a
// waiting GetOrLoad; options are passed through to the REST call.
func (c *CachedSession) Channel(ctx context.Context, channelID string, options ...RequestOption) (*Channel, error) {
	if c.channels == nil {
		return c.s.Channel(channelID, options...)
	}
	return c.channels.GetOrLoad(ctx, channelID, func(ctx context.Context, id string) (*Channel, error) {
		return c.s.Channel(id, options...)
	})
}

// GuildMember returns a guild member: from the TTL cache when fresh,
// otherwise via Session.GuildMember, which is then cached.
func (c *CachedSession) GuildMember(ctx context.Context, guildID, userID string, options ...RequestOption) (*Member, error) {
	if c.members == nil {
		return c.s.GuildMember(guildID, userID, options...)
	}
	return c.members.GetOrLoad(ctx, memberCacheKey{GuildID: guildID, UserID: userID}, func(ctx context.Context, k memberCacheKey) (*Member, error) {
		return c.s.GuildMember(k.GuildID, k.UserID, options...)
	})
}

// ------------------------------------------------------------------------------------------------
// Invalidation
// ------------------------------------------------------------------------------------------------

// InvalidateChannel drops a channel from the cache.
func (c *CachedSession) InvalidateChannel(channelID string) {
	if c.channels != nil {
		c.channels.Delete(channelID)
	}
}

// InvalidateMember drops a guild member from the cache.
func (c *CachedSession) InvalidateMember(guildID, userID string) {
	if c.members != nil {
		c.members.Delete(memberCacheKey{GuildID: guildID, UserID: userID})
	}
}

// InvalidateGuild drops every cached member (and, since channels carry no
// guild-side copy here, only members) of the given guild. It scans the
// member cache keys, so it costs O(n) over the cache.
func (c *CachedSession) InvalidateGuild(guildID string) {
	if c.members == nil {
		return
	}
	for _, k := range c.members.Keys() {
		if k.GuildID == guildID {
			c.members.Delete(k)
		}
	}
}

// InvalidateAll clears both caches.
func (c *CachedSession) InvalidateAll() {
	if c.channels != nil {
		c.channels.Clear()
	}
	if c.members != nil {
		c.members.Clear()
	}
}

// ------------------------------------------------------------------------------------------------
// Observability and lifecycle
// ------------------------------------------------------------------------------------------------

// CachedSessionStats is a metrics snapshot of both caches.
type CachedSessionStats struct {
	ChannelMetrics TTLCacheMetrics
	MemberMetrics  TTLCacheMetrics
	ChannelLen     int
	MemberLen      int
}

// Stats returns a snapshot of both caches' metrics and sizes.
func (c *CachedSession) Stats() CachedSessionStats {
	var st CachedSessionStats
	if c.channels != nil {
		st.ChannelMetrics = c.channels.Metrics()
		st.ChannelLen = c.channels.Len()
	}
	if c.members != nil {
		st.MemberMetrics = c.members.Metrics()
		st.MemberLen = c.members.Len()
	}
	return st
}

// ChannelCache exposes the underlying channel cache (nil when the channel
// cache is disabled), for advanced use such as SaveJSON/LoadJSON.
func (c *CachedSession) ChannelCache() *TTLCache[string, *Channel] {
	return c.channels
}

// MemberCache exposes the underlying member cache (nil when the member
// cache is disabled).
func (c *CachedSession) MemberCache() *TTLCache[memberCacheKey, *Member] {
	return c.members
}

// Close removes the invalidation event handlers and closes both caches.
// The wrapper must not be used for lookups afterwards (lookups would
// still work against the caches, but no auto-invalidation runs). Close is
// idempotent.
func (c *CachedSession) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		removed := c.removed
		c.removed = nil
		c.mu.Unlock()
		for _, remove := range removed {
			remove()
		}
		if c.channels != nil {
			c.channels.Close()
		}
		if c.members != nil {
			c.members.Close()
		}
	})
}
