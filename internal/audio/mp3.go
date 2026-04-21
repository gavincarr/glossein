package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// ErrFFmpegMissing is returned when ffmpeg is not on PATH.
var ErrFFmpegMissing = errors.New("ffmpeg not found on PATH (install: apt install ffmpeg / brew install ffmpeg, or drop --mp3 for WAV output)")

// EncodeMP3Preflight returns ErrFFmpegMissing if ffmpeg is not on PATH.
// Intended to be called before any expensive work when --mp3 is requested.
func EncodeMP3Preflight() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return ErrFFmpegMissing
	}
	return nil
}

// EncodeMP3 transcodes a WAV file to MP3 using ffmpeg's libmp3lame encoder
// at qscale 2 (high VBR, ~190 kbps average). Overwrites outPath if it exists.
func EncodeMP3(ctx context.Context, inPath, outPath string) error {
	if err := EncodeMP3Preflight(); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-loglevel", "error",
		"-i", inPath,
		"-codec:a", "libmp3lame",
		"-qscale:a", "2",
		outPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w\n%s", err, stderr.String())
	}
	return nil
}
