package discordgo

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// newTestVoiceConn returns a fake VoiceConnection that never touches the
// network: audio "sends" are reads from the public OpusSend channel, exactly
// what the real opusSender goroutine consumes.
func newTestVoiceConn(buffer int) *VoiceConnection {
	return &VoiceConnection{
		Ready:     true,
		OpusSend:  make(chan []byte, buffer),
		GuildID:   "g1",
		ChannelID: "c1",
	}
}

// isSilence reports whether a received frame is a trailing-silence frame.
func isSilence(frame []byte) bool {
	return bytes.Equal(frame, OpusSilenceFrame)
}

// receiveWithTimeout reads one frame from ch or fails the test.
func receiveWithTimeout(t *testing.T, ch <-chan []byte, timeout time.Duration) []byte {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a frame")
		return nil
	}
}

func TestVoiceSilenceConstants(t *testing.T) {
	if len(OpusSilenceFrame) != 3 || OpusSilenceFrame[0] != 0xF8 || OpusSilenceFrame[1] != 0xFF || OpusSilenceFrame[2] != 0xFE {
		t.Fatalf("unexpected opus silence frame: %v", OpusSilenceFrame)
	}
	if DefaultVoiceTrailingSilence != 5 {
		t.Fatalf("DefaultVoiceTrailingSilence = %d, want 5", DefaultVoiceTrailingSilence)
	}
	if VoiceSampleRate != 48000 || VoiceFrameSamples != 960 || VoiceFrameDuration != 20*time.Millisecond {
		t.Fatal("voice audio constants drifted from Discord's requirements")
	}
}

func TestWaitVoiceReady(t *testing.T) {
	vc := newTestVoiceConn(1)
	if err := WaitVoiceReady(vc, 0); err != nil {
		t.Fatalf("ready connection must pass immediately: %v", err)
	}

	notReady := &VoiceConnection{}
	if err := WaitVoiceReady(notReady, 40*time.Millisecond); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("expected ErrVoiceNotReady on timeout, got %v", err)
	}
	if err := WaitVoiceReady(notReady, 0); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("expected immediate ErrVoiceNotReady for timeout<=0, got %v", err)
	}

	// Becoming ready mid-wait succeeds.
	late := &VoiceConnection{}
	go func() {
		time.Sleep(30 * time.Millisecond)
		late.Lock()
		late.Ready = true
		late.Unlock()
	}()
	start := time.Now()
	if err := WaitVoiceReady(late, 2*time.Second); err != nil {
		t.Fatalf("expected success once ready, got %v", err)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("WaitVoiceReady returned before readiness was set")
	}

	if err := WaitVoiceReady(nil, time.Second); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("nil connection must return ErrVoiceNotReady, got %v", err)
	}
}

func TestSendOpusFramesNaturalEndFlushesSilence(t *testing.T) {
	vc := newTestVoiceConn(8)
	frames := [][]byte{{0x01}, {0x02}, {0x03}}

	errCh := make(chan error, 1)
	go func() {
		errCh <- SendOpusFrames(vc, frames, WithFrameInterval(time.Millisecond))
	}()

	got := make([][]byte, 0, 8)
	for i := 0; i < len(frames)+DefaultVoiceTrailingSilence; i++ {
		got = append(got, receiveWithTimeout(t, vc.OpusSend, time.Second))
	}

	if err := <-errCh; err != nil {
		t.Fatalf("SendOpusFrames returned %v", err)
	}
	for i, f := range frames {
		if !bytes.Equal(got[i], f) {
			t.Fatalf("frame %d: got %v, want %v", i, got[i], f)
		}
	}
	for i := len(frames); i < len(got); i++ {
		if !isSilence(got[i]) {
			t.Fatalf("frame %d must be silence, got %v", i, got[i])
		}
	}
}

func TestSendOpusFramesPacing(t *testing.T) {
	vc := newTestVoiceConn(4)
	frames := [][]byte{{0x01}, {0x02}, {0x03}, {0x04}}

	// Receive exactly the data frames; the sender runs without trailing
	// silence so its return coincides with the last frame.
	done := make(chan error, 1)
	go func() {
		done <- SendOpusFrames(vc, frames, WithVoiceTrailingSilence(0))
	}()

	start := time.Now()
	for i := 0; i < len(frames); i++ {
		f := receiveWithTimeout(t, vc.OpusSend, 2*time.Second)
		if !bytes.Equal(f, frames[i]) {
			t.Fatalf("frame %d out of order: got %v want %v", i, f, frames[i])
		}
	}
	elapsed := time.Since(start)

	// Frames are paced at the default 20 ms interval: the first send
	// happens after one interval, so four frames take ~80 ms. Only a
	// lower bound is asserted to stay robust on slow machines.
	if elapsed < 4*VoiceFrameDuration-10*time.Millisecond {
		t.Fatalf("4 frames took %s, expected at least ~%s of pacing", elapsed, 4*VoiceFrameDuration)
	}
	if err := <-done; err != nil {
		t.Fatalf("SendOpusFrames returned %v", err)
	}
}

func TestSendOpusFramesNotReady(t *testing.T) {
	vc := &VoiceConnection{Ready: false, OpusSend: make(chan []byte, 1)}
	if err := SendOpusFrames(vc, [][]byte{{0x01}}, WithVoiceReadyTimeout(30*time.Millisecond)); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("expected ErrVoiceNotReady, got %v", err)
	}

	// Ready flag set but no OpusSend channel: same error.
	vc2 := &VoiceConnection{Ready: true}
	if err := SendOpusFrames(vc2, [][]byte{{0x01}}, WithVoiceReadyTimeout(0)); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("expected ErrVoiceNotReady for nil OpusSend, got %v", err)
	}

	if err := SendOpusFrames(nil, [][]byte{{0x01}}, WithVoiceReadyTimeout(0)); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("nil connection must return ErrVoiceNotReady, got %v", err)
	}
}

func TestSendOpusStreamStopGraceful(t *testing.T) {
	vc := newTestVoiceConn(8)

	stop := make(chan struct{})
	var stopOnce sync.Once

	source := make(chan []byte)
	go func() {
		defer close(source)
		for i := 0; i < 100; i++ {
			source <- []byte{byte(i)}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- SendOpusStream(vc, source, WithFrameInterval(2*time.Millisecond), WithStopSignal(stop))
	}()

	// Consume: after three data frames, fire the stop signal.
	dataSeen := 0
	for dataSeen < 3 {
		f := receiveWithTimeout(t, vc.OpusSend, time.Second)
		if !isSilence(f) {
			dataSeen++
		}
	}
	stopOnce.Do(func() { close(stop) })

	// The graceful stop flushes the trailing silence and returns
	// ErrVoiceStopped.
	silenceSeen := 0
	for {
		select {
		case f := <-vc.OpusSend:
			if isSilence(f) {
				silenceSeen++
			}
		case <-time.After(time.Second):
			t.Fatal("stop did not flush trailing silence")
		}
		if silenceSeen == DefaultVoiceTrailingSilence {
			break
		}
	}

	if err := <-errCh; !errors.Is(err, ErrVoiceStopped) {
		t.Fatalf("expected ErrVoiceStopped, got %v", err)
	}
}

func TestSendOpusStreamStopAbrupt(t *testing.T) {
	vc := newTestVoiceConn(8)

	stop := make(chan struct{})
	source := make(chan []byte)
	go func() {
		defer close(source)
		for i := 0; i < 100; i++ {
			source <- []byte{byte(i)}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- SendOpusStream(vc, source,
			WithFrameInterval(2*time.Millisecond), WithStopSignal(stop), WithGracefulStop(false))
	}()

	// Consume two data frames, then stop.
	for i := 0; i < 2; i++ {
		f := receiveWithTimeout(t, vc.OpusSend, time.Second)
		if isSilence(f) {
			i--
		}
	}
	close(stop)

	if err := <-errCh; !errors.Is(err, ErrVoiceStopped) {
		t.Fatalf("expected ErrVoiceStopped, got %v", err)
	}

	// No silence must be flushed on an abrupt stop.
	deadline := time.After(80 * time.Millisecond)
	for {
		select {
		case f := <-vc.OpusSend:
			if isSilence(f) {
				t.Fatal("abrupt stop must not flush silence")
			}
		case <-deadline:
			return
		}
	}
}

func TestSendOpusStreamStopWhileWaiting(t *testing.T) {
	vc := newTestVoiceConn(1)
	stop := make(chan struct{})
	close(stop) // already stopped

	source := make(chan []byte) // never produces
	start := time.Now()
	err := SendOpusStream(vc, source, WithStopSignal(stop), WithGracefulStop(false), WithVoiceReadyTimeout(0))
	if time.Since(start) > time.Second {
		t.Fatal("stop while waiting for input must not block")
	}
	if !errors.Is(err, ErrVoiceStopped) {
		t.Fatalf("expected ErrVoiceStopped, got %v", err)
	}
}

func TestSendPCM16ChunkingAndPadding(t *testing.T) {
	vc := newTestVoiceConn(8)

	var encoded [][]byte
	encode := OpusEncodeFunc(func(pcm []int16) ([]byte, error) {
		encoded = append(encoded, []byte(fmt.Sprintf("len%d", len(pcm))))
		return []byte(fmt.Sprintf("len%d", len(pcm))), nil
	})

	// 2 full frames + an 80-sample remainder.
	pcm := make([]int16, 2*VoiceFrameSamples+80)
	for i := range pcm {
		pcm[i] = int16(i % 97)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- SendPCM16(vc, pcm, encode, WithFrameInterval(time.Millisecond))
	}()

	// 3 encoded frames + 5 silence frames.
	var received [][]byte
	for i := 0; i < 8; i++ {
		received = append(received, receiveWithTimeout(t, vc.OpusSend, time.Second))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendPCM16 returned %v", err)
	}

	want := []string{"len960", "len960", "len960"}
	for i, w := range want {
		if string(received[i]) != w {
			t.Fatalf("frame %d: got %q, want %q", i, received[i], w)
		}
	}
	if len(encoded) != 3 {
		t.Fatalf("expected 3 encode calls, got %d", len(encoded))
	}
	for i := 3; i < len(received); i++ {
		if !isSilence(received[i]) {
			t.Fatalf("frame %d must be silence", i)
		}
	}

	// The final padded frame must carry the remainder samples followed by
	// zeros; verify via a dedicated encode capture.
	var lastPCM []int16
	encode2 := OpusEncodeFunc(func(pcm []int16) ([]byte, error) {
		lastPCM = append([]int16(nil), pcm...)
		return []byte{0xAA}, nil
	})
	tail := make([]int16, 10)
	for i := range tail {
		tail[i] = int16(i + 1)
	}
	small := newTestVoiceConn(2)
	if err := SendPCM16(small, tail, encode2,
		WithFrameInterval(time.Millisecond), WithVoiceTrailingSilence(0)); err != nil {
		t.Fatalf("SendPCM16 tail returned %v", err)
	}
	if len(lastPCM) != VoiceFrameSamples {
		t.Fatalf("padded frame size = %d, want %d", len(lastPCM), VoiceFrameSamples)
	}
	if !reflect.DeepEqual(lastPCM[:10], tail) || !allZero(lastPCM[10:]) {
		t.Fatal("padded frame lost the remainder samples or the zero padding")
	}
}

func allZero(pcm []int16) bool {
	for _, v := range pcm {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestSendPCMStreamBatches(t *testing.T) {
	vc := newTestVoiceConn(8)

	var sizes []int
	encode := OpusEncodeFunc(func(pcm []int16) ([]byte, error) {
		sizes = append(sizes, len(pcm))
		return []byte{0xBB}, nil
	})

	in := make(chan []int16)
	errCh := make(chan error, 1)
	go func() {
		errCh <- SendPCMStream(vc, in, encode, WithFrameInterval(time.Millisecond))
	}()

	// Batch 1: one full frame plus a remainder that carries over.
	in <- make([]int16, VoiceFrameSamples+100)
	// Batch 2: completes the carried-over frame.
	in <- make([]int16, 860)
	close(in)

	// 2 encoded frames + 5 silence.
	for i := 0; i < 7; i++ {
		receiveWithTimeout(t, vc.OpusSend, time.Second)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendPCMStream returned %v", err)
	}
	if len(sizes) != 2 || sizes[0] != VoiceFrameSamples || sizes[1] != VoiceFrameSamples {
		t.Fatalf("unexpected encode sizes: %v", sizes)
	}
}

func TestSendPCMStreamEncodeError(t *testing.T) {
	vc := newTestVoiceConn(2)
	boom := errors.New("encode boom")
	encode := OpusEncodeFunc(func([]int16) ([]byte, error) { return nil, boom })

	in := make(chan []int16, 1)
	in <- make([]int16, VoiceFrameSamples)
	close(in)

	if err := SendPCMStream(vc, in, encode, WithFrameInterval(time.Millisecond), WithVoiceReadyTimeout(0), WithoutSpeaking()); !errors.Is(err, boom) {
		t.Fatalf("expected encoder error to propagate, got %v", err)
	}

	if err := SendPCMStream(vc, nil, nil, WithVoiceReadyTimeout(0)); !errors.Is(err, ErrVoiceNoEncoder) {
		t.Fatalf("expected ErrVoiceNoEncoder, got %v", err)
	}
	if err := SendPCM16(vc, make([]int16, 10), nil, WithVoiceReadyTimeout(0)); !errors.Is(err, ErrVoiceNoEncoder) {
		t.Fatalf("expected ErrVoiceNoEncoder, got %v", err)
	}
}

func TestStopVoice(t *testing.T) {
	vc := newTestVoiceConn(8)
	if err := StopVoice(vc, WithFrameInterval(time.Millisecond)); err != nil {
		t.Fatalf("StopVoice returned %v", err)
	}
	for i := 0; i < DefaultVoiceTrailingSilence; i++ {
		if f := receiveWithTimeout(t, vc.OpusSend, time.Second); !isSilence(f) {
			t.Fatalf("frame %d must be silence", i)
		}
	}

	// A connection without OpusSend has nothing to flush.
	if err := StopVoice(&VoiceConnection{}); err != nil {
		t.Fatalf("StopVoice on unopened connection returned %v", err)
	}
	if err := StopVoice(nil); !errors.Is(err, ErrVoiceNotReady) {
		t.Fatalf("nil connection must return ErrVoiceNotReady, got %v", err)
	}
}

func TestChunkPCM16(t *testing.T) {
	if got := ChunkPCM16([]int16{1, 2, 3, 4, 5}, 2); len(got) != 3 ||
		got[0][0] != 1 || got[0][1] != 2 || got[1][0] != 3 || got[1][1] != 4 ||
		got[2][0] != 5 || got[2][1] != 0 {
		t.Fatalf("unexpected chunking: %v", got)
	}
	if got := ChunkPCM16([]int16{1, 2}, 2); len(got) != 1 || got[0][0] != 1 {
		t.Fatalf("exact division must not pad: %v", got)
	}
	if got := ChunkPCM16(nil, 960); got != nil {
		t.Fatalf("empty input must return nil, got %v", got)
	}
	if got := ChunkPCM16([]int16{1}, 0); got != nil {
		t.Fatalf("frameSize <= 0 must return nil, got %v", got)
	}
}

func TestPadPCM16(t *testing.T) {
	src := []int16{7, 8}
	if got := PadPCM16(src, 2); !reflect.DeepEqual(got, src) {
		t.Fatalf("multiple of frameSize must be returned unchanged: %v", got)
	}
	got := PadPCM16(src, 4)
	if len(got) != 4 || got[0] != 7 || got[1] != 8 || got[2] != 0 || got[3] != 0 {
		t.Fatalf("unexpected padding: %v", got)
	}
	if got := PadPCM16(src, 0); &got[0] != &src[0] {
		t.Fatal("frameSize <= 0 must return the input slice")
	}
}

func TestStereoMonoRoundTrip(t *testing.T) {
	stereo := []int16{10, 20, 30, 40, 50, 61} // odd tail dropped
	mono := StereoToMono16(stereo)
	want := []int16{15, 35, 55}
	if !reflect.DeepEqual(mono, want) {
		t.Fatalf("StereoToMono16 = %v, want %v", mono, want)
	}

	back := MonoToStereo16(want)
	if !reflect.DeepEqual(back, []int16{15, 15, 35, 35, 55, 55}) {
		t.Fatalf("MonoToStereo16 = %v", back)
	}

	// Averaging must not overflow.
	extreme := []int16{32767, 32767}
	if m := StereoToMono16(extreme); m[0] != 32767 {
		t.Fatalf("overflow in averaging: %v", m)
	}
}

func TestResampleLinear16(t *testing.T) {
	// Downsample 48k -> 24k picks every second sample exactly.
	src := []int16{0, 100, 200, 300}
	got, err := ResampleLinear16(src, 48000, 24000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []int16{0, 200}) {
		t.Fatalf("downsample = %v, want [0 200]", got)
	}

	// Upsample 24k -> 48k interpolates midpoints linearly.
	got, err = ResampleLinear16(src, 24000, 48000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []int16{0, 50, 100, 150, 200, 250, 300, 300}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upsample = %v, want %v", got, want)
	}

	// Same rate returns an owned copy.
	same, err := ResampleLinear16(src, 44100, 44100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(same, src) {
		t.Fatalf("identity resample changed the data: %v", same)
	}
	same[0] = 999
	if src[0] != 0 {
		t.Fatal("identity resample must return a copy")
	}

	if _, err := ResampleLinear16(src, 0, 48000); err == nil {
		t.Fatal("expected error for fromRate <= 0")
	}
	if _, err := ResampleLinear16(src, 48000, -1); err == nil {
		t.Fatal("expected error for toRate <= 0")
	}
	if got, _ := ResampleLinear16(nil, 8000, 48000); got != nil {
		t.Fatalf("empty input must return nil, got %v", got)
	}
}

func TestFadeIn16Out16(t *testing.T) {
	pcm := []int16{100, 100, 100, 100}
	FadeIn16(pcm, 4)
	want := []int16{0, 25, 50, 75}
	if !reflect.DeepEqual(pcm, want) {
		t.Fatalf("FadeIn16 = %v, want %v", pcm, want)
	}

	pcm = []int16{100, 100, 100, 100}
	FadeOut16(pcm, 4)
	want = []int16{75, 50, 25, 0}
	if !reflect.DeepEqual(pcm, want) {
		t.Fatalf("FadeOut16 = %v, want %v", pcm, want)
	}

	// Clamped and no-op cases must not panic.
	pcm = []int16{100, 100}
	FadeIn16(pcm, 10) // clamps to len
	if pcm[0] != 0 || pcm[1] != 50 {
		t.Fatalf("clamped fade-in = %v", pcm)
	}
	FadeIn16(pcm, 0)
	FadeOut16(nil, 5)
}

func TestSilencePCM16(t *testing.T) {
	got := SilencePCM16(2, 4)
	if len(got) != 8 || !allZero(got) {
		t.Fatalf("unexpected silence: %v", got)
	}
	if got := SilencePCM16(1, 0); len(got) != VoiceFrameSamples {
		t.Fatalf("frameSize <= 0 must default to VoiceFrameSamples, got %d", len(got))
	}
	if got := SilencePCM16(0, 960); got != nil {
		t.Fatalf("non-positive frame count must return nil, got %v", got)
	}
}

func TestMixPCM16(t *testing.T) {
	dst := []int16{100, 30000, -30000, 5}
	MixPCM16(dst, []int16{100, 30000, -30000, 999})
	want := []int16{200, 32767, -32768, 1004}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("MixPCM16 = %v, want %v", dst, want)
	}

	// Only the overlap is mixed; the longer buffer keeps its tail.
	dst = []int16{1, 2, 3}
	MixPCM16(dst, []int16{10})
	if !reflect.DeepEqual(dst, []int16{11, 2, 3}) {
		t.Fatalf("overlap handling broken: %v", dst)
	}
}

// ------------------------------------------------------------------------------------------------
// DCA tests
// ------------------------------------------------------------------------------------------------

// dcaFrame writes a uint16 little-endian length prefix plus payload.
func dcaFrame(buf *bytes.Buffer, frame []byte) {
	var fl [2]byte
	binary.LittleEndian.PutUint16(fl[:], uint16(len(frame)))
	buf.Write(fl[:])
	buf.Write(frame)
}

// buildDCA0 serialises frames into the dca0 container with a JSON header.
func buildDCA0(t *testing.T, metadata map[string]interface{}, frames [][]byte) []byte {
	t.Helper()
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var buf bytes.Buffer
	buf.WriteString(dca0Magic)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(metaJSON)))
	buf.Write(lenBuf[:])
	buf.Write(metaJSON)
	for _, f := range frames {
		dcaFrame(&buf, f)
	}
	return buf.Bytes()
}

// buildJSONHeader serialises frames behind a newline-terminated JSON header.
func buildJSONHeader(t *testing.T, metadata map[string]interface{}, frames [][]byte) []byte {
	t.Helper()
	metaJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var buf bytes.Buffer
	buf.Write(metaJSON)
	buf.WriteByte('\n')
	for _, f := range frames {
		dcaFrame(&buf, f)
	}
	return buf.Bytes()
}

// buildRaw serialises bare frames.
func buildRaw(frames [][]byte) []byte {
	var buf bytes.Buffer
	for _, f := range frames {
		dcaFrame(&buf, f)
	}
	return buf.Bytes()
}

func checkFrames(t *testing.T, got, want [][]byte) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frames mismatch: got %d frames %v, want %d frames %v", len(got), got, len(want), want)
	}
}

func TestDCAReaderDCA0(t *testing.T) {
	frames := [][]byte{{0xF8, 0xFF, 0xFE}, {0x01, 0x02, 0x03, 0x04}, {0xAA}}
	raw := buildDCA0(t, map[string]interface{}{"opus": "audio", "bitrate": 128000}, frames)

	d, err := NewDCAReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDCAReader: %v", err)
	}
	if d.Format() != "dca0" {
		t.Fatalf("format = %q, want dca0", d.Format())
	}

	var meta struct {
		Opus    string `json:"opus"`
		Bitrate int    `json:"bitrate"`
	}
	if err := d.DecodeMetadata(&meta); err != nil {
		t.Fatalf("DecodeMetadata: %v", err)
	}
	if meta.Opus != "audio" || meta.Bitrate != 128000 {
		t.Fatalf("metadata mismatch: %+v", meta)
	}

	got, err := ReadAllDCA(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadAllDCA: %v", err)
	}
	checkFrames(t, got, frames)
}

func TestDCAReaderJSONHeader(t *testing.T) {
	frames := [][]byte{{0x11}, {0x22, 0x33}}
	raw := buildJSONHeader(t, map[string]interface{}{"tool": "test"}, frames)

	d, err := NewDCAReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDCAReader: %v", err)
	}
	if d.Format() != "json-header" {
		t.Fatalf("format = %q, want json-header", d.Format())
	}
	got, err := ReadAllDCA(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadAllDCA: %v", err)
	}
	checkFrames(t, got, frames)

	// Metadata without a trailing newline still parses.
	rawNoNL := bytes.TrimSuffix(raw, []byte{'\n'})
	got, err = ReadAllDCA(bytes.NewReader(rawNoNL))
	if err != nil {
		t.Fatalf("ReadAllDCA (no trailing newline): %v", err)
	}
	checkFrames(t, got, frames)
}

func TestDCAReaderRaw(t *testing.T) {
	frames := [][]byte{{0x99}, {0x88, 0x77}}
	raw := buildRaw(frames)

	d, err := NewDCAReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("NewDCAReader: %v", err)
	}
	if d.Format() != "raw" {
		t.Fatalf("format = %q, want raw", d.Format())
	}
	if err := d.DecodeMetadata(&struct{}{}); !errors.Is(err, ErrDCANoMetadata) {
		t.Fatalf("expected ErrDCANoMetadata, got %v", err)
	}
	got, err := ReadAllDCA(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadAllDCA: %v", err)
	}
	checkFrames(t, got, frames)
}

func TestDCAReaderErrors(t *testing.T) {
	frames := [][]byte{{0x01, 0x02}}
	raw := buildRaw(frames)

	// Zero-length frame.
	bad := buildRaw([][]byte{{}})
	bad[0], bad[1] = 0, 0
	if _, err := ReadAllDCA(bytes.NewReader(bad)); err == nil || !bytes.Contains([]byte(err.Error()), []byte("zero-length")) {
		t.Fatalf("expected zero-length frame error, got %v", err)
	}

	// Truncated frame payload.
	trunc := append([]byte(nil), raw...)
	trunc = trunc[:len(trunc)-1] // drop one payload byte
	if _, err := ReadAllDCA(bytes.NewReader(trunc)); err == nil || !bytes.Contains([]byte(err.Error()), []byte("short DCA frame")) {
		t.Fatalf("expected short frame error, got %v", err)
	}

	// Truncated frame length.
	if _, err := ReadAllDCA(bytes.NewReader([]byte{0x02})); err == nil || !bytes.Contains([]byte(err.Error()), []byte("frame length")) {
		t.Fatalf("expected frame length error, got %v", err)
	}

	// Empty raw stream yields zero frames and no error.
	got, err := ReadAllDCA(bytes.NewReader(nil))
	if err != nil || len(got) != 0 {
		t.Fatalf("empty stream: frames=%v err=%v", got, err)
	}

	// Oversized DCA0 metadata length.
	oversize := append([]byte("DCA0"), 0xFF, 0xFF, 0xFF, 0xFF)
	if _, err := ReadAllDCA(bytes.NewReader(oversize)); err == nil || !bytes.Contains([]byte(err.Error()), []byte("metadata too large")) {
		t.Fatalf("expected metadata size error, got %v", err)
	}

	// Short DCA0 header.
	if _, err := ReadAllDCA(bytes.NewReader([]byte("DCA0\x01"))); err == nil {
		t.Fatal("expected error for truncated DCA0 header")
	}

	// Invalid metadata JSON.
	badMeta := append([]byte("DCA0"), 0x03, 0, 0, 0)
	badMeta = append(badMeta, '{', ',', '}')
	if _, err := ReadAllDCA(bytes.NewReader(badMeta)); err == nil || !bytes.Contains([]byte(err.Error()), []byte("invalid DCA metadata")) {
		t.Fatalf("expected invalid metadata error, got %v", err)
	}

	// Nil source.
	if _, err := NewDCAReader(nil); err == nil {
		t.Fatal("expected error for nil source")
	}
}

func TestReadDCAFile(t *testing.T) {
	frames := [][]byte{{0xDE, 0xAD}, {0xBE, 0xEF}}
	raw := buildRaw(frames)
	path := filepath.Join(t.TempDir(), "sound.dca")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write temp dca: %v", err)
	}

	got, err := ReadDCAFile(path)
	if err != nil {
		t.Fatalf("ReadDCAFile: %v", err)
	}
	checkFrames(t, got, frames)

	if _, err := ReadDCAFile(filepath.Join(t.TempDir(), "missing.dca")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSendDCAEndToEnd(t *testing.T) {
	// dca0 file -> reader -> paced send into a fake VoiceConnection.
	frames := [][]byte{{0x0A}, {0x0B}, {0x0C}}
	raw := buildDCA0(t, map[string]interface{}{"opus": "audio"}, frames)

	parsed, err := ReadAllDCA(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadAllDCA: %v", err)
	}

	vc := newTestVoiceConn(8)
	errCh := make(chan error, 1)
	go func() {
		errCh <- SendOpusFrames(vc, parsed, WithFrameInterval(time.Millisecond))
	}()

	var received [][]byte
	for i := 0; i < len(frames)+DefaultVoiceTrailingSilence; i++ {
		received = append(received, receiveWithTimeout(t, vc.OpusSend, time.Second))
	}
	if err := <-errCh; err != nil {
		t.Fatalf("SendOpusFrames returned %v", err)
	}
	checkFrames(t, received[:len(frames)], frames)
	for i := len(frames); i < len(received); i++ {
		if !isSilence(received[i]) {
			t.Fatalf("frame %d must be silence", i)
		}
	}
}
