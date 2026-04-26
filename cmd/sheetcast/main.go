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
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	flags "github.com/jessevdk/go-flags"
	"github.com/lmittmann/tint"

	"github.com/gavincarr/glossein/internal/audio"
	"github.com/gavincarr/glossein/internal/sheets"
	"github.com/gavincarr/glossein/internal/tts"
)

const (
	maxConcurrency    = 16
	maxPlainTextBytes = 5000 // Google TTS plain-text per-request limit
	defaultVoice      = "en-US-Neural2-D"
	defaultOutDir     = "data"
	fallbackOutDir    = "/tmp"
	fallbackOutBase   = "sheetcast"
	outputDirEnv      = "SHEETCAST_OUTPUT_DIR"
)

type options struct {
	Column       string        `long:"column" default:"B" value-name:"SPEC" description:"column: letter (A, B, ...), 1-based number, or #Header"`
	NoSkipHeader bool          `long:"no-skip-header" description:"treat row 1 as data (default: row 1 is treated as header)"`
	Voice        string        `long:"voice" value-name:"NAME" description:"Google TTS voice (default: derived from a 2-letter language code in the column header, else en-US-Neural2-D)"`
	Lang         string        `long:"lang" value-name:"CODE" description:"language code override (default: inferred from voice)"`
	Project      string        `long:"project" env:"GOOGLE_CLOUD_PROJECT" value-name:"ID" description:"GCP project for billing/quota (overrides ADC quota-project setting)"`
	Rate         int           `long:"rate" default:"24000" value-name:"HZ" description:"sample rate in Hz"`
	Mode         string        `long:"mode" short:"m" choice:"listen" choice:"shadow" choice:"repeat" description:"gap mode (default: 'repeat' when --repeat>1, else 'listen')"`
	Gap          time.Duration `long:"gap" value-name:"DURATION" description:"override preset inter-sentence silence (e.g. 2.5s)"`
	Repeat       int           `long:"repeat" short:"r" default:"1" value-name:"N" description:"play each sentence N times before moving to the next"`
	Lead         time.Duration `long:"lead" default:"100ms" value-name:"DURATION" description:"silence before first sentence"`
	Trail        time.Duration `long:"trail" default:"1s" value-name:"DURATION" description:"silence after last sentence"`
	OutputDir    string        `long:"output-dir" short:"d" value-name:"DIR" description:"output directory (default: $SHEETCAST_OUTPUT_DIR, else ./data if it exists, else /tmp)"`
	Out          string        `long:"out" short:"o" value-name:"PATH" description:"output file (default: <slugified-sheet-title>.wav). A bare filename is joined with --output-dir; a path overrides --output-dir."`
	MP3          bool          `long:"mp3" description:"encode final output as MP3 via ffmpeg"`
	KeepWAV      bool          `long:"keep-wav" description:"with --mp3, keep the intermediate WAV"`
	Concurrency  int           `long:"concurrency" default:"4" value-name:"N" description:"parallel TTS requests (1..16)"`
	DryRun       bool          `long:"dry-run" description:"print the sentences that would be synthesised and exit"`
	Verbose      []bool        `long:"verbose" short:"v" description:"verbose output to stderr (-v: info, -vv: debug)"`
}

func main() {
	setupLogger(slog.LevelWarn)
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func setupLogger(level slog.Level) {
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:      level,
		TimeFormat: "15:04:05",
	})))
}

func logLevel(opts options) slog.Level {
	switch {
	case len(opts.Verbose) >= 2:
		return slog.LevelDebug
	case len(opts.Verbose) == 1 || opts.DryRun:
		return slog.LevelInfo
	}
	return slog.LevelWarn
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

	setupLogger(logLevel(opts))

	if opts.Concurrency < 1 || opts.Concurrency > maxConcurrency {
		return fmt.Errorf("--concurrency must be between 1 and %d, got %d", maxConcurrency, opts.Concurrency)
	}
	if opts.Rate < 8000 {
		return fmt.Errorf("--rate must be at least 8000, got %d", opts.Rate)
	}
	if opts.Repeat < 1 {
		return fmt.Errorf("--repeat must be at least 1, got %d", opts.Repeat)
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

	slog.Debug("fetching sheet", "id", id)
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
	if voiceSource != "" {
		slog.Info("voice resolved", "voice", voice, "source", voiceSource)
	}

	slog.Info("sentences parsed", "count", len(sentences), "column", opts.Column)
	if opts.DryRun {
		for i, s := range sentences {
			fmt.Printf("%3d  %s\n", i+1, s)
		}
		return nil
	}

	cfg := outputConfig{
		out:           opts.Out,
		outputDir:     opts.OutputDir,
		outputDirEnv:  os.Getenv(outputDirEnv),
		dataDirExists: dirExists(defaultOutDir),
	}
	base, err := resolveOutputBase(cfg, func() (string, error) {
		return sheets.FetchTitle(ctx, id)
	})
	if err != nil {
		return err
	}
	slog.Debug("output base resolved", "path", base)
	wavPath, mp3Path := resolveOutputPaths(base, opts.MP3)

	if opts.MP3 {
		if err := audio.EncodeMP3Preflight(); err != nil {
			return err
		}
	}

	if opts.Project != "" {
		slog.Debug("gcp quota project", "project", opts.Project)
	}
	client, err := tts.New(ctx, voice, opts.Lang, opts.Project, opts.Rate)
	if err != nil {
		return err
	}
	defer client.Close()

	clips, err := synthesizeAll(ctx, client, sentences, opts.Concurrency)
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

	pcmChunks = repeatChunks(pcmChunks, opts.Repeat)

	if err := writeWAV(wavPath, format, pcmChunks, gap, opts.Lead, opts.Trail); err != nil {
		return err
	}
	slog.Info("wrote", "path", wavPath)

	if opts.MP3 {
		if err := audio.EncodeMP3(ctx, wavPath, mp3Path); err != nil {
			return err
		}
		slog.Info("wrote", "path", mp3Path)
		if !opts.KeepWAV {
			if err := os.Remove(wavPath); err != nil {
				slog.Warn("could not remove intermediate WAV", "path", wavPath, "err", err)
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

// resolveMode returns the explicit --mode if set, otherwise picks "repeat" when
// --repeat>1 (so repeated sentences inherit the repeat-mode gap by default) and
// "listen" in all other cases.
func resolveMode(opts options) string {
	if opts.Mode != "" {
		return opts.Mode
	}
	if opts.Repeat > 1 {
		return "repeat"
	}
	return "listen"
}

func resolveGap(opts options) (time.Duration, error) {
	if opts.Gap > 0 {
		return opts.Gap, nil
	}
	mode := resolveMode(opts)
	if d, ok := gapModes[mode]; ok {
		return d, nil
	}
	return 0, fmt.Errorf("unknown --mode %q (valid: %s)", mode, strings.Join(modeNames(), ", "))
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

// outputConfig captures all inputs resolveOutputBase needs. Keeping this as
// a plain struct (rather than reading env/filesystem inside the function) lets
// tests drive every branch without touching the real environment.
type outputConfig struct {
	out           string // --out value (empty if unset)
	outputDir     string // --output-dir CLI value (empty if unset; env is NOT folded in here)
	outputDirEnv  string // $SHEETCAST_OUTPUT_DIR (empty if unset)
	dataDirExists bool   // whether ./data exists
}

// resolveOutputBase returns the full output path (pre-extension-adjustment)
// by combining --out, --output-dir, $SHEETCAST_OUTPUT_DIR, and the ./data /
// /tmp fallbacks. Errors if the user explicitly set both --output-dir and an
// --out containing a directory — env-supplied --output-dir is never "explicit"
// so it silently yields to --out.
//
// fetchTitle is only invoked when --out is empty (title drives the filename).
func resolveOutputBase(cfg outputConfig, fetchTitle func() (string, error)) (string, error) {
	outHasPath := cfg.out != "" && strings.ContainsRune(cfg.out, filepath.Separator)

	if outHasPath && cfg.outputDir != "" {
		return "", errors.New("cannot specify both --output-dir and an --out that contains a directory path")
	}
	if outHasPath {
		return cfg.out, nil
	}

	var dir string
	switch {
	case cfg.outputDir != "":
		dir = cfg.outputDir
	case cfg.outputDirEnv != "":
		dir = cfg.outputDirEnv
	case cfg.dataDirExists:
		dir = defaultOutDir
	default:
		dir = fallbackOutDir
	}

	filename := cfg.out
	if filename == "" {
		title, err := fetchTitle()
		if err != nil {
			slog.Warn("could not fetch sheet title for default output path", "err", err)
		}
		base := slugify(title)
		if base == "" {
			base = fallbackOutBase
		}
		filename = base + ".wav"
	}
	return filepath.Join(dir, filename), nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// slugify maps a free-form title to a filename-safe base: Unicode letters
// and digits are preserved, everything else collapses to underscores, and
// leading/trailing underscores are trimmed.
func slugify(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
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

func synthesizeAll(ctx context.Context, client *tts.Client, sentences []string, concurrency int) ([][]byte, error) {
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
			doneMu.Lock()
			done++
			n := done
			doneMu.Unlock()
			preview := text
			if len(preview) > 50 {
				preview = preview[:50] + "…"
			}
			slog.Debug("synthesised", "n", n, "total", len(sentences), "text", preview)
		}(i, s)
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}

// repeatChunks duplicates each chunk n times in place, preserving order. n<=1
// is a no-op. The same byte slice is reused across copies — writeWAV only reads.
func repeatChunks(chunks [][]byte, n int) [][]byte {
	if n <= 1 {
		return chunks
	}
	out := make([][]byte, 0, len(chunks)*n)
	for _, c := range chunks {
		for i := 0; i < n; i++ {
			out = append(out, c)
		}
	}
	return out
}

func writeWAV(path string, f audio.Format, chunks [][]byte, gap, lead, trail time.Duration) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating output directory %q: %w", dir, err)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return audio.WriteWAV(file, f, chunks, gap, lead, trail)
}
