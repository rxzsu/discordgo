package discordgo

import (
	"sync/atomic"
	"time"
)

// GatewayMetrics is a snapshot of one Session's Gateway activity. Counters
// accumulate over the lifetime of the session and may change during a snapshot.
type GatewayMetrics struct {
	ShardID int

	// ReconnectAttempts counts automatic connection attempts, including failures.
	ReconnectAttempts uint64
	// ReconnectSuccesses counts successful automatic connections.
	ReconnectSuccesses uint64
	// ResumeAttempts counts RESUME writes attempted during a handshake.
	ResumeAttempts uint64
	// ResumeSuccesses counts RESUMED events that confirm a pending RESUME.
	ResumeSuccesses uint64

	// IdentifyWait is the cumulative time spent in the shard identify scheduler,
	// including cancelled or rejected waits. It excludes writing the packet.
	IdentifyWait time.Duration
	// IdentifyBudgetRejections counts attempts rejected by the shard scheduler.
	IdentifyBudgetRejections uint64

	// HeartbeatLatency is the latest acknowledged round-trip time. It is zero
	// until the first heartbeat acknowledgement.
	HeartbeatLatency time.Duration
}

type gatewayMetrics struct {
	reconnectAttempts        atomic.Uint64
	reconnectSuccesses       atomic.Uint64
	resumeAttempts           atomic.Uint64
	resumeSuccesses          atomic.Uint64
	resumePending            atomic.Bool
	identifyWait             atomic.Int64
	identifyBudgetRejections atomic.Uint64
	heartbeatLatency         atomic.Int64
}

// GatewayMetrics returns a snapshot suitable for polling from another goroutine.
func (s *Session) GatewayMetrics() GatewayMetrics {
	return GatewayMetrics{
		ShardID:                  s.ShardID,
		ReconnectAttempts:        s.gatewayMetrics.reconnectAttempts.Load(),
		ReconnectSuccesses:       s.gatewayMetrics.reconnectSuccesses.Load(),
		ResumeAttempts:           s.gatewayMetrics.resumeAttempts.Load(),
		ResumeSuccesses:          s.gatewayMetrics.resumeSuccesses.Load(),
		IdentifyWait:             time.Duration(s.gatewayMetrics.identifyWait.Load()),
		IdentifyBudgetRejections: s.gatewayMetrics.identifyBudgetRejections.Load(),
		HeartbeatLatency:         s.HeartbeatLatency(),
	}
}

// GatewayMetrics returns a snapshot for each registered shard, ordered by ID.
// It returns an empty slice when there are no registered shards.
func (m *ShardManager) GatewayMetrics() []GatewayMetrics {
	m.mu.Lock()
	sessions := m.sessionsLocked()
	m.mu.Unlock()

	metrics := make([]GatewayMetrics, len(sessions))
	for i, s := range sessions {
		metrics[i] = s.GatewayMetrics()
	}
	return metrics
}
