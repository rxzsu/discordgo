package discordgo

// This file implements streaming helpers on top of the public VoiceConnection
// API (Ready, OpusSend, Speaking): paced sending of pre-encoded opus frames,
// PCM-to-opus plumbing behind a user supplied encoder, trailing-silence
// flushing and graceful stops. Everything is standard library only and works
// against the same channels the library's own opusSender consumes, so no
// existing voice behaviour is changed.

import (
	"errors"
	"time"
)

// Discord voice audio constants (48 kHz mono, 20 ms frames of 960 samples,
// see https://discord.com/developers/docs/topics/voice-connections).
const (
	// VoiceSampleRate is the only sample rate the Discord voice gateway accepts.
	VoiceSampleRate = 48000
	// VoiceFrameSamples is the number of mono PCM samples per 20 ms frame.
	VoiceFrameSamples = 960
	// VoiceFrameDuration is the wall-clock duration of a single opus frame.
	VoiceFrameDuration = 20 * time.Millisecond
)

// OpusSilenceFrame is the three byte opus frame that decodes to silence.
// Flushing a run of them after a stream makes Discord clients stop replaying
// the tail of the previous audio instead of holding the last frame.
var OpusSilenceFrame = []byte{0xF8, 0xFF, 0xFE}

// DefaultVoiceTrailingSilence is the number of silence frames flushed after
// a stream ends, matching the five-frame convention of common DCA tooling
// (5 frames = 100 ms of silence).
const DefaultVoiceTrailingSilence = 5

var (
	// ErrVoiceNotReady is returned when audio is sent to a VoiceConnection
	// that has not finished its handshake (Ready false or no OpusSend channel).
	ErrVoiceNotReady = errors.New("discordgo: voice connection is not ready")
	// ErrVoiceStopped is returned when a voice send was aborted through the
	// stop signal of VoiceSendOptions.
	ErrVoiceStopped = errors.New("discordgo: voice send was stopped")
	// ErrVoiceNoEncoder is returned when a PCM send is started without an
	// opus encoder function.
	ErrVoiceNoEncoder = errors.New("discordgo: nil opus encoder for PCM send")
)

// WaitVoiceReady blocks until vc.Ready turns true or the timeout elapses.
// A timeout <= 0 performs a single immediate check. It returns
// ErrVoiceNotReady when the connection is not (yet) ready.
//
// The library creates vc.OpusSend during the voice handshake, so a ready
// connection always has a usable OpusSend channel.
func WaitVoiceReady(vc *VoiceConnection, timeout time.Duration) error {
	if vc == nil {
		return ErrVoiceNotReady
	}
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		vc.RLock()
		ready := vc.Ready
		vc.RUnlock()
		if ready {
			return nil
		}
		if deadline.IsZero() || !time.Now().Before(deadline) {
			return ErrVoiceNotReady
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// VoiceSendOptions tunes the voice streaming helpers.
type VoiceSendOptions struct {
	// FrameInterval is the pause between two frames. It defaults to
	// VoiceFrameDuration (20 ms); values <= 0 disable pacing, making the
	// internal OpusSend backpressure the only rate limit.
	FrameInterval time.Duration
	// TrailingSilence is the number of OpusSilenceFrame copies flushed
	// after the stream ends. It defaults to DefaultVoiceTrailingSilence;
	// 0 disables the flush.
	TrailingSilence int
	// SkipSpeaking leaves the speaking indicator untouched. By default the
	// senders call vc.Speaking(true) when starting and vc.Speaking(false)
	// when finished; both calls are best effort and never fail the send.
	SkipSpeaking bool
	// GracefulStop makes an aborted send (stop signal fired) flush the
	// trailing silence before returning ErrVoiceStopped, so audio that is
	// already queued ends cleanly. It defaults to true.
	GracefulStop bool
	// ReadyTimeout bounds the initial wait for vc.Ready. It defaults to
	// 10 seconds; a negative value skips the wait entirely (the caller
	// promises the connection is ready).
	ReadyTimeout time.Duration
	// FlushTimeout bounds each individual silence frame write of the
	// trailing flush so a dead consumer cannot hang the sender forever.
	// It defaults to 5 seconds.
	FlushTimeout time.Duration
	// Stop, when closed, aborts the send between frames.
	Stop <-chan struct{}
}

// VoiceSendOption mutates VoiceSendOptions.
type VoiceSendOption func(*VoiceSendOptions)

// WithFrameInterval overrides the pause between frames. A value <= 0 disables
// pacing entirely (frames are pushed as fast as OpusSend accepts them).
func WithFrameInterval(d time.Duration) VoiceSendOption {
	return func(o *VoiceSendOptions) { o.FrameInterval = d }
}

// WithVoiceTrailingSilence overrides the number of trailing silence frames.
// 0 disables the flush.
func WithVoiceTrailingSilence(n int) VoiceSendOption {
	return func(o *VoiceSendOptions) { o.TrailingSilence = n }
}

// WithoutSpeaking stops the senders from touching the speaking indicator.
func WithoutSpeaking() VoiceSendOption {
	return func(o *VoiceSendOptions) { o.SkipSpeaking = true }
}

// WithGracefulStop selects whether an aborted send still flushes trailing
// silence (true, the default) or returns immediately (false).
func WithGracefulStop(graceful bool) VoiceSendOption {
	return func(o *VoiceSendOptions) { o.GracefulStop = graceful }
}

// WithVoiceReadyTimeout overrides the initial wait for vc.Ready. A negative
// value skips the wait entirely.
func WithVoiceReadyTimeout(d time.Duration) VoiceSendOption {
	return func(o *VoiceSendOptions) { o.ReadyTimeout = d }
}

// WithVoiceFlushTimeout overrides the per-frame bound of the silence flush.
func WithVoiceFlushTimeout(d time.Duration) VoiceSendOption {
	return func(o *VoiceSendOptions) { o.FlushTimeout = d }
}

// WithStopSignal installs a stop channel: closing it aborts the send. The
// senders return ErrVoiceStopped when the signal fires.
func WithStopSignal(stop <-chan struct{}) VoiceSendOption {
	return func(o *VoiceSendOptions) { o.Stop = stop }
}

// voiceSendOptions resolves defaults and applies opts.
func voiceSendOptions(opts []VoiceSendOption) VoiceSendOptions {
	o := VoiceSendOptions{
		FrameInterval:   VoiceFrameDuration,
		TrailingSilence: DefaultVoiceTrailingSilence,
		GracefulStop:    true,
		ReadyTimeout:    10 * time.Second,
		FlushTimeout:    5 * time.Second,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// OpusEncodeFunc encodes one frame of VoiceFrameSamples mono 48 kHz 16-bit
// PCM samples into an opus frame. Implementations usually wrap an external
// opus encoder (for example gopkg.in/hraban/opus); the library itself stays
// dependency free. The encoder must not retain the pcm slice beyond the call.
type OpusEncodeFunc func(pcm []int16) ([]byte, error)

// SendOpusFrames sends pre-encoded opus frames to vc.OpusSend, one frame per
// frame interval, after waiting for the connection to become ready. After
// the last frame it flushes trailing silence and clears the speaking
// indicator (unless disabled). It returns ErrVoiceStopped when the stop
// signal fired before the stream finished, and never returns nil OpusSend
// errors: sending is aborted with ErrVoiceNotReady instead.
func SendOpusFrames(vc *VoiceConnection, frames [][]byte, opts ...VoiceSendOption) error {
	o := voiceSendOptions(opts)
	return sendVoiceStreamFrames(vc, frames, &o)
}

// SendOpusStream behaves like SendOpusFrames but consumes frames from ch
// until it is closed or the stop signal fires (the stop is observed while
// waiting for the next frame as well).
func SendOpusStream(vc *VoiceConnection, ch <-chan []byte, opts ...VoiceSendOption) error {
	o := voiceSendOptions(opts)
	next := func() ([]byte, bool, error) {
		select {
		case f, ok := <-ch:
			return f, ok, nil
		case <-o.Stop:
			return nil, false, ErrVoiceStopped
		}
	}
	return sendVoiceStream(vc, next, &o)
}

// SendPCMStream converts PCM samples received from ch into opus frames via
// encode and streams them with SendOpusStream semantics. Incoming batches of
// any size are re-chunked into VoiceFrameSamples frames (16-bit mono 48 kHz);
// a partial trailing frame is zero-padded before it is encoded. The slice
// handed to encode is only valid for the duration of the call. A nil encoder
// returns ErrVoiceNoEncoder.
func SendPCMStream(vc *VoiceConnection, ch <-chan []int16, encode OpusEncodeFunc, opts ...VoiceSendOption) error {
	if encode == nil {
		return ErrVoiceNoEncoder
	}
	o := voiceSendOptions(opts)
	buf := make([]int16, 0, VoiceFrameSamples)
	next := func() ([]byte, bool, error) {
		for {
			// Emit a full frame when enough samples accumulated.
			if len(buf) >= VoiceFrameSamples {
				frame := buf[:VoiceFrameSamples]
				enc, err := encode(frame)
				copy(buf, buf[VoiceFrameSamples:])
				buf = buf[:len(buf)-VoiceFrameSamples]
				return enc, true, err
			}
			select {
			case batch, ok := <-ch:
				if !ok {
					if len(buf) == 0 {
						return nil, false, nil
					}
					// Pad the remainder to a full frame; the padded
					// copy is fresh, so retaining it is safe.
					enc, err := encode(PadPCM16(buf, VoiceFrameSamples))
					buf = buf[:0]
					return enc, false, err
				}
				buf = append(buf, batch...)
			case <-o.Stop:
				return nil, false, ErrVoiceStopped
			}
		}
	}
	return sendVoiceStream(vc, next, &o)
}

// SendPCM16 chunks a complete PCM buffer into VoiceFrameSamples frames,
// encodes each one with encode and streams the result with SendOpusFrames
// semantics. The final partial frame is zero-padded.
func SendPCM16(vc *VoiceConnection, pcm []int16, encode OpusEncodeFunc, opts ...VoiceSendOption) error {
	if encode == nil {
		return ErrVoiceNoEncoder
	}
	o := voiceSendOptions(opts)
	frames := make([][]byte, 0, len(pcm)/VoiceFrameSamples+1)
	for off := 0; off < len(pcm); off += VoiceFrameSamples {
		end := off + VoiceFrameSamples
		if end > len(pcm) {
			end = len(pcm)
		}
		enc, err := encode(PadPCM16(pcm[off:end], VoiceFrameSamples))
		if err != nil {
			return err
		}
		frames = append(frames, enc)
	}
	return sendVoiceStreamFrames(vc, frames, &o)
}

// StopVoice gracefully ends playback on vc: it flushes trailing silence
// frames at the frame interval and clears the speaking indicator. It is a
// no-op when the connection has no OpusSend channel yet (nothing can be
// playing) and is safe to call after any send. The trailing silence count
// and interval come from opts (defaults: DefaultVoiceTrailingSilence frames
// at VoiceFrameDuration).
func StopVoice(vc *VoiceConnection, opts ...VoiceSendOption) error {
	if vc == nil {
		return ErrVoiceNotReady
	}
	o := voiceSendOptions(opts)
	vc.RLock()
	sendCh := vc.OpusSend
	vc.RUnlock()
	if sendCh == nil {
		return nil // nothing can be playing on a not-yet-opened connection
	}
	var tickC <-chan time.Time
	if o.FrameInterval > 0 {
		t := time.NewTicker(o.FrameInterval)
		defer t.Stop()
		tickC = t.C
	}
	flushVoiceSilence(sendCh, &o, tickC)
	if !o.SkipSpeaking {
		if err := vc.Speaking(false); err != nil {
			vc.log(LogDebug, "voiceutils: speaking(false) error, %s", err)
		}
	}
	return nil
}

// sendVoiceStreamFrames is SendOpusFrames with pre-resolved options.
func sendVoiceStreamFrames(vc *VoiceConnection, frames [][]byte, o *VoiceSendOptions) error {
	i := 0
	next := func() ([]byte, bool, error) {
		if i >= len(frames) {
			return nil, false, nil
		}
		f := frames[i]
		i++
		return f, true, nil
	}
	return sendVoiceStream(vc, next, o)
}

// sendVoiceStream is the shared core of the senders: wait for readiness,
// snapshot OpusSend, raise the speaking indicator, pull frames through next
// and push them into the channel paced by the options, then clean up. next
// returns (frame, true, nil) per frame, (nil, false, nil) on natural end of
// stream or an error to abort with.
func sendVoiceStream(vc *VoiceConnection, next func() ([]byte, bool, error), o *VoiceSendOptions) error {
	if vc == nil {
		return ErrVoiceNotReady
	}
	if o.ReadyTimeout >= 0 {
		if err := WaitVoiceReady(vc, o.ReadyTimeout); err != nil {
			return err
		}
	}
	// Snapshot the channel: OpusSend is allocated once during the voice
	// handshake and stays stable across reconnects (only recreated if
	// nil), so a snapshot is safe for the duration of the stream.
	vc.RLock()
	sendCh := vc.OpusSend
	vc.RUnlock()
	if sendCh == nil {
		return ErrVoiceNotReady
	}

	if !o.SkipSpeaking {
		if err := vc.Speaking(true); err != nil {
			// Best effort: keep going, frames still flow through
			// OpusSend even when the speaking websocket write fails.
			vc.log(LogWarning, "voiceutils: speaking(true) error, %s", err)
		}
	}

	err := pacedVoiceSend(sendCh, next, o)

	if !o.SkipSpeaking {
		if serr := vc.Speaking(false); serr != nil {
			vc.log(LogDebug, "voiceutils: speaking(false) error, %s", serr)
		}
	}
	return err
}

// pacedVoiceSend pulls frames through next and pushes them into sendCh,
// pausing one FrameInterval between frames. It returns nil on natural end of
// stream and the next() error on abort.
func pacedVoiceSend(sendCh chan<- []byte, next func() ([]byte, bool, error), o *VoiceSendOptions) error {
	var tickC <-chan time.Time
	if o.FrameInterval > 0 {
		t := time.NewTicker(o.FrameInterval)
		defer t.Stop()
		tickC = t.C
	}

	abort := func() error {
		if o.GracefulStop {
			flushVoiceSilence(sendCh, o, tickC)
		}
		return ErrVoiceStopped
	}

	for {
		frame, more, err := next()
		if err != nil {
			return err
		}
		if !more {
			break
		}
		if tickC != nil {
			select {
			case <-tickC:
			case <-o.Stop:
				return abort()
			}
		}
		select {
		case sendCh <- frame:
		case <-o.Stop:
			return abort()
		}
	}

	flushVoiceSilence(sendCh, o, tickC)
	return nil
}

// flushVoiceSilence pushes o.TrailingSilence copies of OpusSilenceFrame into
// sendCh so remote decoders drain cleanly. The flush deliberately ignores
// the stop signal (it is the graceful part of a stop) but each write is
// bounded by FlushTimeout so a dead consumer cannot hang the caller forever.
func flushVoiceSilence(sendCh chan<- []byte, o *VoiceSendOptions, tickC <-chan time.Time) {
	if o.TrailingSilence <= 0 {
		return
	}
	timeout := o.FlushTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	for i := 0; i < o.TrailingSilence; i++ {
		if tickC != nil {
			select {
			case <-tickC:
			case <-time.After(timeout):
				return
			}
		}
		select {
		case sendCh <- OpusSilenceFrame:
		case <-time.After(timeout):
			return
		}
	}
}
