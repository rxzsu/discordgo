package discordgo

// Pure PCM helper functions for 16-bit mono audio at Discord's voice sample
// rate (48 kHz, VoiceFrameSamples samples per 20 ms frame). They are small
// and dependency free so they can be chained in front of an opus encoder
// (see OpusEncodeFunc) or used to pre-process DCA-free material.

import "fmt"

// ChunkPCM16 splits pcm into frames of frameSize samples. The final partial
// frame is zero-padded to frameSize so every returned frame is full size,
// as required by opus encoders that only accept constant frame sizes.
// All frames except the final one alias pcm. frameSize <= 0 returns nil.
func ChunkPCM16(pcm []int16, frameSize int) [][]int16 {
	if frameSize <= 0 || len(pcm) == 0 {
		return nil
	}
	frames := make([][]int16, 0, (len(pcm)+frameSize-1)/frameSize)
	for off := 0; off < len(pcm); off += frameSize {
		end := off + frameSize
		if end <= len(pcm) {
			// Full frame: alias pcm with capped capacity so appends on
			// the frame never clobber the next one.
			frames = append(frames, pcm[off:end:end])
		} else {
			frames = append(frames, PadPCM16(pcm[off:], frameSize))
		}
	}
	return frames
}

// PadPCM16 zero-pads pcm to a multiple of frameSize samples, as opus
// encoders require full frames. When len(pcm) is already a multiple of
// frameSize (or frameSize <= 0) pcm is returned unchanged; otherwise a new
// slice holding the original samples followed by silence is returned.
func PadPCM16(pcm []int16, frameSize int) []int16 {
	if frameSize <= 0 {
		return pcm
	}
	rem := len(pcm) % frameSize
	if rem == 0 {
		return pcm
	}
	out := make([]int16, len(pcm)+frameSize-rem)
	copy(out, pcm)
	return out // int16 zero value is silence
}

// StereoToMono16 averages an interleaved stereo stream (L,R,L,R,...) down to
// mono, clamping to the int16 range. An odd trailing sample is dropped.
func StereoToMono16(stereo []int16) []int16 {
	mono := make([]int16, len(stereo)/2)
	for i := range mono {
		sum := int32(stereo[2*i]) + int32(stereo[2*i+1])
		mono[i] = int16(sum / 2)
	}
	return mono
}

// MonoToStereo16 duplicates a mono stream into an interleaved stereo one.
func MonoToStereo16(mono []int16) []int16 {
	stereo := make([]int16, len(mono)*2)
	for i, v := range mono {
		stereo[2*i], stereo[2*i+1] = v, v
	}
	return stereo
}

// ResampleLinear16 converts the sample rate of src using linear
// interpolation. Both rates must be positive. When the rates are equal a
// copy of src is returned so callers always own the result.
func ResampleLinear16(src []int16, fromRate, toRate int) ([]int16, error) {
	if fromRate <= 0 || toRate <= 0 {
		return nil, fmt.Errorf("discordgo: invalid sample rates %d -> %d", fromRate, toRate)
	}
	if len(src) == 0 {
		return nil, nil
	}
	if fromRate == toRate {
		return append([]int16(nil), src...), nil
	}
	outLen := len(src) * toRate / fromRate
	out := make([]int16, outLen)
	ratio := float64(fromRate) / float64(toRate)
	last := len(src) - 1
	for i := range out {
		pos := float64(i) * ratio
		i0 := int(pos)
		if i0 >= last {
			out[i] = src[last]
			continue
		}
		frac := pos - float64(i0)
		a := float64(src[i0])
		b := float64(src[i0+1])
		out[i] = clampInt16(a + (b-a)*frac)
	}
	return out, nil
}

// clampInt16 clamps a float sample into the int16 range.
func clampInt16(v float64) int16 {
	switch {
	case v >= 32767:
		return 32767
	case v <= -32768:
		return -32768
	default:
		return int16(v)
	}
}

// FadeIn16 applies a linear fade-in over the first fadeSamples samples,
// in place. fadeSamples <= 0 is a no-op and values beyond len(pcm) are
// clamped to the buffer length.
func FadeIn16(pcm []int16, fadeSamples int) {
	if fadeSamples <= 0 || len(pcm) == 0 {
		return
	}
	if fadeSamples > len(pcm) {
		fadeSamples = len(pcm)
	}
	for i := 0; i < fadeSamples; i++ {
		gain := int64(i)
		pcm[i] = int16(int64(pcm[i]) * gain / int64(fadeSamples))
	}
}

// FadeOut16 applies a linear fade-out over the last fadeSamples samples,
// in place, ending at silence. fadeSamples <= 0 is a no-op and values beyond
// len(pcm) are clamped to the buffer length.
func FadeOut16(pcm []int16, fadeSamples int) {
	if fadeSamples <= 0 || len(pcm) == 0 {
		return
	}
	if fadeSamples > len(pcm) {
		fadeSamples = len(pcm)
	}
	start := len(pcm) - fadeSamples
	for i := 0; i < fadeSamples; i++ {
		gain := int64(fadeSamples - 1 - i)
		pcm[start+i] = int16(int64(pcm[start+i]) * gain / int64(fadeSamples))
	}
}

// SilencePCM16 returns frameCount frames of digital silence; frameSize <= 0
// defaults to VoiceFrameSamples. A non-positive frameCount returns nil.
func SilencePCM16(frameCount, frameSize int) []int16 {
	if frameCount <= 0 {
		return nil
	}
	if frameSize <= 0 {
		frameSize = VoiceFrameSamples
	}
	return make([]int16, frameCount*frameSize)
}

// MixPCM16 adds add into dst sample by sample with saturation, in place,
// over the overlap of the two buffers. It is useful for overlaying sounds
// (jingles over music) before encoding.
func MixPCM16(dst, add []int16) {
	n := len(dst)
	if len(add) < n {
		n = len(add)
	}
	for i := 0; i < n; i++ {
		sum := int32(dst[i]) + int32(add[i])
		switch {
		case sum > 32767:
			dst[i] = 32767
		case sum < -32768:
			dst[i] = -32768
		default:
			dst[i] = int16(sum)
		}
	}
}
