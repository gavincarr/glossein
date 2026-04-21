// Package audio concatenates LINEAR16 PCM clips from Google Cloud TTS
// into a single canonical WAV file with configurable silence between clips.
//
// The WAV-stripping code walks RIFF chunks properly rather than blindly
// skipping 44 bytes: Google's TTS responses occasionally contain a LIST
// info chunk between "fmt " and "data". Hearing clicks at clip boundaries
// means StripWAVHeader left metadata bytes in the PCM stream.
package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// Format describes a PCM stream. All concatenated clips in a single WAV
// must share the same Format.
type Format struct {
	SampleRate    int
	Channels      int
	BitsPerSample int
}

// DefaultFormat matches what Google TTS returns for LINEAR16 at the
// Neural2 default sample rate: 24 kHz mono 16-bit.
var DefaultFormat = Format{SampleRate: 24000, Channels: 1, BitsPerSample: 16}

func (f Format) bytesPerSample() int { return f.Channels * (f.BitsPerSample / 8) }

func (f Format) byteRate() int { return f.SampleRate * f.bytesPerSample() }

// Silence returns a zero-filled byte slice of the right length for d at f.
// Duration is rounded to whole samples to keep block alignment.
func Silence(d time.Duration, f Format) []byte {
	if d <= 0 {
		return nil
	}
	samples := int((d.Seconds() * float64(f.SampleRate)) + 0.5)
	return make([]byte, samples*f.bytesPerSample())
}

// StripWAVHeader parses a RIFF/WAVE container and returns just the PCM
// samples from the "data" chunk plus the parsed Format. It handles optional
// chunks (LIST, fact, etc.) that may appear before "data".
func StripWAVHeader(wav []byte) ([]byte, Format, error) {
	var f Format
	if len(wav) < 12 {
		return nil, f, fmt.Errorf("wav too short: %d bytes", len(wav))
	}
	if string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, f, fmt.Errorf("not a RIFF/WAVE file: %q / %q", wav[0:4], wav[8:12])
	}

	pos := 12
	haveFmt := false
	for pos+8 <= len(wav) {
		id := string(wav[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))
		bodyStart := pos + 8
		bodyEnd := bodyStart + size
		if bodyEnd > len(wav) {
			return nil, f, fmt.Errorf("chunk %q claims %d bytes but only %d remain", id, size, len(wav)-bodyStart)
		}

		switch id {
		case "fmt ":
			if size < 16 {
				return nil, f, fmt.Errorf("fmt chunk too small: %d bytes", size)
			}
			audioFormat := binary.LittleEndian.Uint16(wav[bodyStart : bodyStart+2])
			if audioFormat != 1 {
				return nil, f, fmt.Errorf("non-PCM audio format %d", audioFormat)
			}
			f.Channels = int(binary.LittleEndian.Uint16(wav[bodyStart+2 : bodyStart+4]))
			f.SampleRate = int(binary.LittleEndian.Uint32(wav[bodyStart+4 : bodyStart+8]))
			f.BitsPerSample = int(binary.LittleEndian.Uint16(wav[bodyStart+14 : bodyStart+16]))
			haveFmt = true
		case "data":
			if !haveFmt {
				return nil, f, fmt.Errorf(`"data" chunk preceded "fmt " chunk`)
			}
			return wav[bodyStart:bodyEnd], f, nil
		}

		// Chunks are padded to an even byte count; advance accordingly.
		advance := size
		if advance%2 == 1 {
			advance++
		}
		pos = bodyStart + advance
	}
	return nil, f, fmt.Errorf(`no "data" chunk found`)
}

// WriteWAV concatenates PCM chunks (all in Format f) into a single canonical
// PCM WAV written to w. Silence of `gap` is inserted between adjacent chunks;
// `lead` and `trail` are silence before the first and after the last chunk.
func WriteWAV(w io.Writer, f Format, chunks [][]byte, gap, lead, trail time.Duration) error {
	if f.SampleRate <= 0 || f.Channels <= 0 || f.BitsPerSample <= 0 {
		return fmt.Errorf("invalid format: %+v", f)
	}

	leadSilence := Silence(lead, f)
	trailSilence := Silence(trail, f)
	gapSilence := Silence(gap, f)

	var dataSize int
	dataSize += len(leadSilence) + len(trailSilence)
	for i, c := range chunks {
		dataSize += len(c)
		if i > 0 {
			dataSize += len(gapSilence)
		}
	}

	// Canonical 44-byte PCM header: RIFF(12) + fmt (24) + data header(8)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(header[22:24], uint16(f.Channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(f.SampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(f.byteRate()))
	binary.LittleEndian.PutUint16(header[32:34], uint16(f.bytesPerSample()))
	binary.LittleEndian.PutUint16(header[34:36], uint16(f.BitsPerSample))
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))

	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(leadSilence) > 0 {
		if _, err := w.Write(leadSilence); err != nil {
			return err
		}
	}
	for i, c := range chunks {
		if i > 0 && len(gapSilence) > 0 {
			if _, err := w.Write(gapSilence); err != nil {
				return err
			}
		}
		if _, err := w.Write(c); err != nil {
			return err
		}
	}
	if len(trailSilence) > 0 {
		if _, err := w.Write(trailSilence); err != nil {
			return err
		}
	}
	return nil
}

// WriteWAVBytes is a bytes.Buffer convenience wrapper around WriteWAV.
func WriteWAVBytes(f Format, chunks [][]byte, gap, lead, trail time.Duration) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteWAV(&buf, f, chunks, gap, lead, trail); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
