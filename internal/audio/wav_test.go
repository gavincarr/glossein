package audio

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
	"time"
)

func TestSilence_Length(t *testing.T) {
	// Default format: 24000 samples/s * 1 channel * 2 bytes = 48000 bytes/s
	if got := len(Silence(time.Second, DefaultFormat)); got != 48000 {
		t.Errorf("1s silence = %d bytes, want 48000", got)
	}
	if got := len(Silence(500*time.Millisecond, DefaultFormat)); got != 24000 {
		t.Errorf("500ms silence = %d bytes, want 24000", got)
	}
	if got := len(Silence(3500*time.Millisecond, DefaultFormat)); got != 168000 {
		t.Errorf("shadow-gap silence = %d bytes, want 168000", got)
	}
	// All-zeroes check (first byte is sufficient)
	for i, b := range Silence(100*time.Millisecond, DefaultFormat) {
		if b != 0 {
			t.Fatalf("silence[%d] = %d, want 0", i, b)
			break
		}
	}
	if got := Silence(0, DefaultFormat); got != nil {
		t.Errorf("zero-duration silence = %v, want nil", got)
	}
}

func TestSilence_StereoAlignment(t *testing.T) {
	stereo := Format{SampleRate: 44100, Channels: 2, BitsPerSample: 16}
	b := Silence(time.Second, stereo)
	if len(b) != 44100*2*2 {
		t.Errorf("stereo 1s = %d, want %d", len(b), 44100*2*2)
	}
	// Must be sample-aligned (divisible by blockAlign=4)
	if len(b)%4 != 0 {
		t.Errorf("stereo silence length %d not block-aligned", len(b))
	}
}

// synthWAV builds a synthetic WAV with optional LIST chunk before "data".
func synthWAV(t *testing.T, f Format, pcm []byte, withLIST bool) []byte {
	t.Helper()
	var buf bytes.Buffer

	// fmt chunk body (16 bytes for PCM)
	fmtBody := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtBody[0:2], 1)
	binary.LittleEndian.PutUint16(fmtBody[2:4], uint16(f.Channels))
	binary.LittleEndian.PutUint32(fmtBody[4:8], uint32(f.SampleRate))
	binary.LittleEndian.PutUint32(fmtBody[8:12], uint32(f.SampleRate*f.Channels*f.BitsPerSample/8))
	binary.LittleEndian.PutUint16(fmtBody[12:14], uint16(f.Channels*f.BitsPerSample/8))
	binary.LittleEndian.PutUint16(fmtBody[14:16], uint16(f.BitsPerSample))

	// Total RIFF size will be fixed up at the end.
	buf.WriteString("RIFF")
	buf.Write(make([]byte, 4)) // placeholder
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(len(fmtBody)))
	buf.Write(fmtBody)

	if withLIST {
		// A small LIST/INFO chunk: INFO + INAM + text "Google TTS"
		listBody := []byte{'I', 'N', 'F', 'O',
			'I', 'N', 'A', 'M',
			0x0B, 0, 0, 0, // 11 bytes of text+NUL
			'G', 'o', 'o', 'g', 'l', 'e', ' ', 'T', 'T', 'S', 0,
			0, // pad to even
		}
		buf.WriteString("LIST")
		binary.Write(&buf, binary.LittleEndian, uint32(len(listBody)))
		buf.Write(listBody)
	}

	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)

	// Fix up RIFF size
	out := buf.Bytes()
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}

func TestStripWAVHeader_Canonical(t *testing.T) {
	pcm := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	wav := synthWAV(t, DefaultFormat, pcm, false)

	gotPCM, gotFmt, err := StripWAVHeader(wav)
	if err != nil {
		t.Fatalf("StripWAVHeader: %v", err)
	}
	if !reflect.DeepEqual(gotPCM, pcm) {
		t.Errorf("pcm = %x, want %x", gotPCM, pcm)
	}
	if gotFmt != DefaultFormat {
		t.Errorf("fmt = %+v, want %+v", gotFmt, DefaultFormat)
	}
}

func TestStripWAVHeader_WithLISTChunk(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	wav := synthWAV(t, DefaultFormat, pcm, true)

	gotPCM, gotFmt, err := StripWAVHeader(wav)
	if err != nil {
		t.Fatalf("StripWAVHeader with LIST: %v", err)
	}
	if !reflect.DeepEqual(gotPCM, pcm) {
		t.Errorf("pcm = %x, want %x (LIST chunk bled into data?)", gotPCM, pcm)
	}
	if gotFmt != DefaultFormat {
		t.Errorf("fmt = %+v, want %+v", gotFmt, DefaultFormat)
	}
}

func TestStripWAVHeader_Malformed(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("short"),
		append([]byte("NOPE"), make([]byte, 20)...),
	}
	for i, c := range cases {
		if _, _, err := StripWAVHeader(c); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestWriteWAV_RoundTrip(t *testing.T) {
	chunk1 := bytes.Repeat([]byte{0x10, 0x20}, 10) // 20 bytes = 10 samples mono-16
	chunk2 := bytes.Repeat([]byte{0x30, 0x40}, 5)  // 10 bytes = 5 samples

	out, err := WriteWAVBytes(DefaultFormat, [][]byte{chunk1, chunk2},
		100*time.Millisecond, 50*time.Millisecond, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("WriteWAVBytes: %v", err)
	}

	leadBytes := len(Silence(50*time.Millisecond, DefaultFormat))
	gapBytes := len(Silence(100*time.Millisecond, DefaultFormat))
	trailBytes := len(Silence(200*time.Millisecond, DefaultFormat))
	expectedData := leadBytes + len(chunk1) + gapBytes + len(chunk2) + trailBytes

	if got := len(out) - 44; got != expectedData {
		t.Errorf("data section = %d, want %d", got, expectedData)
	}

	// Round-trip through the parser
	pcm, f, err := StripWAVHeader(out)
	if err != nil {
		t.Fatalf("StripWAVHeader round-trip: %v", err)
	}
	if f != DefaultFormat {
		t.Errorf("round-trip fmt = %+v, want %+v", f, DefaultFormat)
	}
	if len(pcm) != expectedData {
		t.Errorf("round-trip pcm len = %d, want %d", len(pcm), expectedData)
	}

	// Confirm chunk bytes are intact at the right offsets
	if !bytes.Equal(pcm[leadBytes:leadBytes+len(chunk1)], chunk1) {
		t.Errorf("chunk1 not found at expected offset")
	}
	chunk2Offset := leadBytes + len(chunk1) + gapBytes
	if !bytes.Equal(pcm[chunk2Offset:chunk2Offset+len(chunk2)], chunk2) {
		t.Errorf("chunk2 not found at expected offset")
	}

	// Verify canonical RIFF/data size fields
	riffSize := binary.LittleEndian.Uint32(out[4:8])
	if int(riffSize) != 36+expectedData {
		t.Errorf("RIFF chunkSize = %d, want %d", riffSize, 36+expectedData)
	}
	dataSize := binary.LittleEndian.Uint32(out[40:44])
	if int(dataSize) != expectedData {
		t.Errorf("data subchunk2Size = %d, want %d", dataSize, expectedData)
	}
}

func TestWriteWAV_NoGapsOrChunks(t *testing.T) {
	// Zero chunks is valid — produces a silence-only WAV.
	out, err := WriteWAVBytes(DefaultFormat, nil, 0, 200*time.Millisecond, 0)
	if err != nil {
		t.Fatalf("WriteWAVBytes empty: %v", err)
	}
	if len(out) != 44+len(Silence(200*time.Millisecond, DefaultFormat)) {
		t.Errorf("unexpected size: %d", len(out))
	}
}
