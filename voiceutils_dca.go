package discordgo

// DCA stream parsing, standard library only (bufio + encoding/json + encoding/binary).
//
// DCA files hold pre-encoded opus frames for direct playback into
// VoiceConnection.OpusSend. Three layouts are recognised; in all of them a
// frame is a uint16 little-endian length followed by that many bytes of opus
// data:
//
//   - "dca0": the four byte magic "DCA0", a uint32 little-endian metadata
//     length, that many bytes of JSON metadata, then frames. This is the
//     container produced by the bwmarrin/dca tool.
//   - "json-header": a single line of JSON metadata terminated by '\n',
//     then frames. Several DCA producers emit this variant.
//   - "raw": frames from the very first byte (what the bundled
//     examples/voice_playback parser expects).
//
// Feed the frames to SendOpusFrames or SendOpusStream for paced playback.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	dca0Magic          = "DCA0"
	dcaMaxMetadataSize = 1 << 20 // 1 MiB is far above any real metadata block
	dcaMaxFrameSize    = 1 << 20 // safety cap for future 4-byte-length variants
)

// ErrDCANoMetadata is returned by DCAReader.DecodeMetadata for raw streams
// that carry no header metadata.
var ErrDCANoMetadata = errors.New("discordgo: DCA stream has no metadata")

// DCAReader reads opus frames from a DCA stream. It sniffs the header format
// in NewDCAReader and then yields one frame per Next call.
type DCAReader struct {
	br       *bufio.Reader
	format   string
	metadata json.RawMessage
}

// NewDCAReader wraps r and sniffs the DCA header. The detected layout and
// metadata become available through Format and Metadata. A nil r returns an
// error; otherwise the only failure modes are corrupt headers.
func NewDCAReader(r io.Reader) (*DCAReader, error) {
	if r == nil {
		return nil, errors.New("discordgo: nil DCA source")
	}
	br := bufio.NewReader(r)
	d := &DCAReader{br: br, format: "raw"}

	head, err := br.Peek(len(dca0Magic))
	switch {
	case err == nil && string(head) == dca0Magic:
		if _, err := br.Discard(len(dca0Magic)); err != nil {
			return nil, fmt.Errorf("discordgo: DCA0 magic discard: %w", err)
		}
		var n [4]byte
		if _, err := io.ReadFull(br, n[:]); err != nil {
			return nil, fmt.Errorf("discordgo: short DCA0 metadata length: %w", err)
		}
		metaLen := binary.LittleEndian.Uint32(n[:])
		if metaLen > dcaMaxMetadataSize {
			return nil, fmt.Errorf("discordgo: DCA metadata too large (%d bytes)", metaLen)
		}
		meta := make([]byte, metaLen)
		if _, err := io.ReadFull(br, meta); err != nil {
			return nil, fmt.Errorf("discordgo: short DCA0 metadata: %w", err)
		}
		d.format = "dca0"
		d.metadata = json.RawMessage(meta)
	case err == nil && head[0] == '{':
		line, rerr := br.ReadBytes('\n')
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return nil, fmt.Errorf("discordgo: reading DCA metadata line: %w", rerr)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return nil, errors.New("discordgo: empty DCA metadata line")
		}
		d.format = "json-header"
		d.metadata = json.RawMessage(line)
	default:
		// Not enough bytes to sniff or no known header: treat everything
		// as raw frames.
	}

	if len(d.metadata) > 0 && !json.Valid(d.metadata) {
		return nil, fmt.Errorf("discordgo: invalid DCA metadata JSON: %.64s", d.metadata)
	}
	return d, nil
}

// Format returns the detected layout: "dca0", "json-header" or "raw".
func (d *DCAReader) Format() string {
	return d.format
}

// Metadata returns the raw JSON header metadata, or nil for raw streams.
func (d *DCAReader) Metadata() json.RawMessage {
	return d.metadata
}

// DecodeMetadata unmarshals the header metadata into v. Raw streams return
// ErrDCANoMetadata. A typical metadata object for dca0 files carries
// "opus"/"format" fields, but its shape is producer defined.
func (d *DCAReader) DecodeMetadata(v interface{}) error {
	if len(d.metadata) == 0 {
		return ErrDCANoMetadata
	}
	return json.Unmarshal(d.metadata, v)
}

// Next returns the next opus frame. The returned slice is freshly allocated
// per call and owned by the caller. io.EOF signals a clean end of stream.
func (d *DCAReader) Next() ([]byte, error) {
	var n [2]byte
	if _, err := io.ReadFull(d.br, n[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("discordgo: reading DCA frame length: %w", err)
	}
	frameLen := int(binary.LittleEndian.Uint16(n[:]))
	if frameLen == 0 {
		return nil, errors.New("discordgo: zero-length DCA frame")
	}
	if frameLen > dcaMaxFrameSize {
		return nil, fmt.Errorf("discordgo: DCA frame too large (%d bytes)", frameLen)
	}
	frame := make([]byte, frameLen)
	if _, err := io.ReadFull(d.br, frame); err != nil {
		return nil, fmt.Errorf("discordgo: short DCA frame: %w", err)
	}
	return frame, nil
}

// ReadAllDCA reads every frame of the DCA stream and returns them in order.
func ReadAllDCA(r io.Reader) ([][]byte, error) {
	d, err := NewDCAReader(r)
	if err != nil {
		return nil, err
	}
	var frames [][]byte
	for {
		f, err := d.Next()
		if errors.Is(err, io.EOF) {
			return frames, nil
		}
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
	}
}

// ReadDCAFile is ReadAllDCA for a file path.
func ReadDCAFile(path string) ([][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadAllDCA(f)
}
