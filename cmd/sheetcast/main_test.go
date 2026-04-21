package main

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestResolveVoice(t *testing.T) {
	italianCSV := []byte("EN,IT\nhello,ciao\n")
	unlabeledCSV := []byte("prompt,answer\nhello,ciao\n")
	emptyCSV := []byte("")

	cases := []struct {
		name       string
		opts       options
		csv        []byte
		wantVoice  string
		wantSource string // "" means explicit; "default" means fallback; else substring match
	}{
		{"explicit voice wins over header", options{Voice: "custom-X-Y-Z", Column: "B"}, italianCSV, "custom-X-Y-Z", ""},
		{"header IT maps to it-IT-Neural2-A", options{Column: "B"}, italianCSV, "it-IT-Neural2-A", "auto-detected"},
		{"header EN maps to en-US-Neural2-D", options{Column: "A"}, italianCSV, "en-US-Neural2-D", "auto-detected"},
		{"non-lang-code header falls back to default", options{Column: "A"}, unlabeledCSV, defaultVoice, "default"},
		{"empty CSV falls back to default", options{Column: "A"}, emptyCSV, defaultVoice, "default"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			voice, source := resolveVoice(tc.opts, tc.csv)
			if voice != tc.wantVoice {
				t.Errorf("voice = %q, want %q", voice, tc.wantVoice)
			}
			switch tc.wantSource {
			case "":
				if source != "" {
					t.Errorf("source = %q, want empty (explicit)", source)
				}
			case "default":
				if source != "default" {
					t.Errorf("source = %q, want %q", source, "default")
				}
			default:
				if !strings.Contains(source, tc.wantSource) {
					t.Errorf("source = %q, want to contain %q", source, tc.wantSource)
				}
			}
		})
	}
}

func TestResolveOutputPaths(t *testing.T) {
	cases := []struct {
		name    string
		out     string
		mp3     bool
		wantWAV string
		wantMP3 string
	}{
		{"wav default, no ext added", "sheetcast.wav", false, "sheetcast.wav", ""},
		{"wav adds .wav when missing", "deck", false, "deck.wav", ""},
		{"wav preserves path", "/tmp/deck.wav", false, "/tmp/deck.wav", ""},
		{"mp3 from bare name", "deck", true, "deck.wav", "deck.mp3"},
		{"mp3 from .wav name", "deck.wav", true, "deck.wav", "deck.mp3"},
		{"mp3 from .mp3 name", "deck.mp3", true, "deck.wav", "deck.mp3"},
		{"mp3 preserves directory", "/tmp/deck", true, "/tmp/deck.wav", "/tmp/deck.mp3"},
		{"mp3 from path/.wav", "/tmp/deck.wav", true, "/tmp/deck.wav", "/tmp/deck.mp3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotWAV, gotMP3 := resolveOutputPaths(tc.out, tc.mp3)
			if gotWAV != tc.wantWAV {
				t.Errorf("wav = %q, want %q", gotWAV, tc.wantWAV)
			}
			if gotMP3 != tc.wantMP3 {
				t.Errorf("mp3 = %q, want %q", gotMP3, tc.wantMP3)
			}
		})
	}
}

func TestLogLevel(t *testing.T) {
	cases := []struct {
		name string
		opts options
		want slog.Level
	}{
		{"quiet default", options{}, slog.LevelWarn},
		{"dry-run alone → info", options{DryRun: true}, slog.LevelInfo},
		{"-v → info", options{Verbose: []bool{true}}, slog.LevelInfo},
		{"-v and dry-run → info", options{Verbose: []bool{true}, DryRun: true}, slog.LevelInfo},
		{"-vv → debug", options{Verbose: []bool{true, true}}, slog.LevelDebug},
		{"-vvv → debug", options{Verbose: []bool{true, true, true}}, slog.LevelDebug},
		{"-vv and dry-run → debug", options{Verbose: []bool{true, true}, DryRun: true}, slog.LevelDebug},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logLevel(tc.opts); got != tc.want {
				t.Errorf("logLevel = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveGap(t *testing.T) {
	cases := []struct {
		name    string
		opts    options
		want    time.Duration
		wantErr bool
	}{
		{"listen default", options{Mode: "listen"}, 1200 * time.Millisecond, false},
		{"shadow", options{Mode: "shadow"}, 3500 * time.Millisecond, false},
		{"drill", options{Mode: "drill"}, 6000 * time.Millisecond, false},
		{"--gap overrides mode", options{Mode: "listen", Gap: 2500 * time.Millisecond}, 2500 * time.Millisecond, false},
		{"--gap overrides even with unknown mode", options{Mode: "nope", Gap: 500 * time.Millisecond}, 500 * time.Millisecond, false},
		{"unknown mode without --gap errors", options{Mode: "nope"}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveGap(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("gap = %v, want %v", got, tc.want)
			}
		})
	}
}
