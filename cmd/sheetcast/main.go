// sheetcast generates a single audio file from the target-language sentences
// in a publicly-shared Google Sheet, using Google Cloud TTS.
//
// Authentication: `gcloud auth application-default login` once. Billing flows
// directly to the caller's own GCP project.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	flags "github.com/jessevdk/go-flags"

	"github.com/gavincarr/glossein/internal/audio"
	"github.com/gavincarr/glossein/internal/sheets"
	"github.com/gavincarr/glossein/internal/tts"
)

const (
	maxConcurrency    = 16
	maxPlainTextBytes = 5000 // Google TTS plain-text per-request limit
	defaultVoice      = "en-US-Neural2-D"
)

type options struct {
	Column       string        `long:"column" default:"B" value-name:"SPEC" description:"column: letter (A, B, ...), 1-based number, or #Header"`
	NoSkipHeader bool          `long:"no-skip-header" description:"treat row 1 as data (default: row 1 is treated as header)"`
	Voice        string        `long:"voice" value-name:"NAME" description:"Google TTS voice (default: derived from a 2-letter language code in the column header, else en-US-Neural2-D)"`
	Lang         string        `long:"lang" value-name:"CODE" description:"language code override (default: inferred from voice)"`
	Rate         int           `long:"rate" default:"24000" value-name:"HZ" description:"sample rate in Hz"`
	Mode         string        `long:"mode" short:"m" default:"listen" choice:"listen" choice:"shadow" choice:"drill" description:"gap mode"`
	Gap          time.Duration `long:"gap" value-name:"DURATION" description:"override preset inter-sentence silence (e.g. 2.5s)"`
	Lead         time.Duration `long:"lead" default:"500ms" value-name:"DURATION" description:"silence before first sentence"`
	Trail        time.Duration `long:"trail" default:"1s" value-name:"DURATION" description:"silence after last sentence"`
	Out          string        `long:"out" short:"o" default:"sheetcast.wav" value-name:"PATH" description:"output path (extension adjusted for --mp3)"`
	MP3          bool          `long:"mp3" description:"encode final output as MP3 via ffmpeg"`
	KeepWAV      bool          `long:"keep-wav" description:"with --mp3, keep the intermediate WAV"`
	Concurrency  int           `long:"concurrency" default:"4" value-name:"N" description:"parallel TTS requests (1..16)"`
	DryRun       bool          `long:"dry-run" description:"print the sentences that would be synthesised and exit"`
	Verbose      bool          `long:"verbose" short:"v" description:"progress to stderr"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sheetcast: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var opts options
	parser := flags.NewParser(&opts, flags.HelpFlag|flags.PassDoubleDash)
	parser.Usage = "[flags] <sheet-url-or-id>"
	parser.LongDescription = "Synthesise a Google Sheets column to one audio file using Google Cloud TTS. " +
		"Billing flows to the caller's own GCP project via Application Default Credentials; " +
		"run 'gcloud auth application-default login' once before using."

	args, err := parser.Parse()
	if err != nil {
		var ferr *flags.Error
		if errors.As(err, &ferr) && ferr.Type == flags.ErrHelp {
			parser.WriteHelp(os.Stdout)
			return nil
		}
		return err
	}
	if len(args) != 1 {
		return errors.New("expected exactly one positional argument: the sheet URL or ID")
	}
	input := args[0]

	if opts.Concurrency < 1 || opts.Concurrency > maxConcurrency {
		return fmt.Errorf("--concurrency must be between 1 and %d, got %d", maxConcurrency, opts.Concurrency)
	}
	if opts.Rate < 8000 {
		return fmt.Errorf("--rate must be at least 8000, got %d", opts.Rate)
	}

	gap, err := resolveGap(opts)
	if err != nil {
		return err
	}

	id, err := sheets.ExtractID(input)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "Fetching sheet %s\n", id)
	}
	csv, err := sheets.FetchCSV(ctx, id)
	if err != nil {
		return err
	}

	skipHeader := !opts.NoSkipHeader
	sentences, err := sheets.ParseColumn(csv, opts.Column, skipHeader)
	if err != nil {
		return err
	}

	if err := validateSentences(sentences); err != nil {
		return err
	}

	voice, voiceSource := resolveVoice(opts, csv)
	if voiceSource != "" && (opts.Verbose || opts.DryRun) {
		fmt.Fprintf(os.Stderr, "Voice %s (%s)\n", voice, voiceSource)
	}

	if opts.Verbose || opts.DryRun {
		fmt.Fprintf(os.Stderr, "%d sentence(s) in column %s\n", len(sentences), opts.Column)
	}
	if opts.DryRun {
		for i, s := range sentences {
			fmt.Printf("%3d  %s\n", i+1, s)
		}
		return nil
	}

	wavPath, mp3Path := resolveOutputPaths(opts.Out, opts.MP3)

	if opts.MP3 {
		if err := audio.EncodeMP3Preflight(); err != nil {
			return err
		}
	}

	client, err := tts.New(ctx, voice, opts.Lang, opts.Rate)
	if err != nil {
		return err
	}
	defer client.Close()

	clips, err := synthesizeAll(ctx, client, sentences, opts.Concurrency, opts.Verbose)
	if err != nil {
		return err
	}

	pcmChunks := make([][]byte, len(clips))
	format := audio.Format{SampleRate: opts.Rate, Channels: 1, BitsPerSample: 16}
	for i, c := range clips {
		pcm, f, err := audio.StripWAVHeader(c)
		if err != nil {
			return fmt.Errorf("sentence %d: stripping WAV header: %w", i+1, err)
		}
		if f != format {
			return fmt.Errorf("sentence %d returned unexpected format %+v (expected %+v)", i+1, f, format)
		}
		pcmChunks[i] = pcm
	}

	if err := writeWAV(wavPath, format, pcmChunks, gap, opts.Lead, opts.Trail); err != nil {
		return err
	}
	if opts.Verbose {
		fmt.Fprintf(os.Stderr, "Wrote %s\n", wavPath)
	}

	if opts.MP3 {
		if err := audio.EncodeMP3(ctx, wavPath, mp3Path); err != nil {
			return err
		}
		if opts.Verbose {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", mp3Path)
		}
		if !opts.KeepWAV {
			if err := os.Remove(wavPath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not remove intermediate WAV: %v\n", err)
			}
		}
		fmt.Println(mp3Path)
	} else {
		fmt.Println(wavPath)
	}
	return nil
}

// resolveVoice picks a voice with this precedence:
//  1. --voice explicit
//  2. column header looks like a 2-letter language code (e.g. "IT", "en") → derived voice
//  3. hardcoded default
//
// Returns the voice and a short provenance string (empty if explicit).
func resolveVoice(opts options, csv []byte) (string, string) {
	if opts.Voice != "" {
		return opts.Voice, ""
	}
	if header, err := sheets.HeaderOf(csv, opts.Column); err == nil && header != "" {
		if v, ok := tts.VoiceFromLangCode(header); ok {
			return v, fmt.Sprintf("auto-detected from column header %q", header)
		}
	}
	return defaultVoice, "default"
}

func resolveGap(opts options) (time.Duration, error) {
	if opts.Gap > 0 {
		return opts.Gap, nil
	}
	if d, ok := gapModes[opts.Mode]; ok {
		return d, nil
	}
	return 0, fmt.Errorf("unknown --mode %q (valid: %s)", opts.Mode, strings.Join(modeNames(), ", "))
}

func validateSentences(sentences []string) error {
	for i, s := range sentences {
		if len(s) > maxPlainTextBytes {
			preview := s
			if len(preview) > 80 {
				preview = preview[:80] + "…"
			}
			return fmt.Errorf("sentence %d exceeds Google TTS plain-text limit (%d > %d bytes): %q", i+1, len(s), maxPlainTextBytes, preview)
		}
	}
	return nil
}

func resolveOutputPaths(out string, mp3 bool) (wavPath, mp3Path string) {
	if !mp3 {
		if filepath.Ext(out) == "" {
			return out + ".wav", ""
		}
		return out, ""
	}
	base := strings.TrimSuffix(strings.TrimSuffix(out, ".mp3"), ".wav")
	if filepath.Ext(out) == "" {
		base = out
	}
	return base + ".wav", base + ".mp3"
}

func synthesizeAll(ctx context.Context, client *tts.Client, sentences []string, concurrency int, verbose bool) ([][]byte, error) {
	results := make([][]byte, len(sentences))
	sem := make(chan struct{}, concurrency)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
		done     int
		doneMu   sync.Mutex
	)

	for i, s := range sentences {
		wg.Add(1)
		go func(idx int, text string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}
			wav, err := client.Synthesize(ctx, text)
			if err != nil {
				errOnce.Do(func() {
					firstErr = err
					cancel()
				})
				return
			}
			results[idx] = wav
			if verbose {
				doneMu.Lock()
				done++
				n := done
				doneMu.Unlock()
				preview := text
				if len(preview) > 50 {
					preview = preview[:50] + "…"
				}
				fmt.Fprintf(os.Stderr, "[%d/%d] %s\n", n, len(sentences), preview)
			}
		}(i, s)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

func writeWAV(path string, f audio.Format, chunks [][]byte, gap, lead, trail time.Duration) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return audio.WriteWAV(file, f, chunks, gap, lead, trail)
}
