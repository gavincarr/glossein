# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```
# Build sheetcast binary (lands at ./sheetcast, gitignored)
go build -o sheetcast ./cmd/sheetcast

# Run flashcards web app locally (serves embedded index.html + /csv proxy)
go run ./app/flashcards -port 8080

# All tests
go test ./...

# Single package
go test ./internal/sheets/...

# Single test (regex on name)
go test -run TestResolveVoice ./cmd/sheetcast/...

# Vet + tidy
go vet ./...
go mod tidy
```

GCP setup for sheetcast (one-time):

```
gcloud auth application-default login
gcloud services enable texttospeech.googleapis.com
gcloud auth application-default set-quota-project YOUR_PROJECT
```

The repo uses direnv (`.envrc` → `.envrc.local` for shared, `.envrc` for local/secret). `GOOGLE_CLOUD_PROJECT` and `GOOGLE_APPLICATION_CREDENTIALS` are the two env vars worth knowing.

## Architecture

**Two user-facing artifacts from one underlying concept — publicly-shared Google Sheets fetched as CSV via the unauthenticated `/gviz/tq?tqx=out:csv` endpoint, column A = prompt/source, column B = answer/target.**

### flashcards (`app/flashcards/`)

Static web app deployed to Netlify at glossein.netlify.app. **All sheet logic lives in JavaScript inside `index.html`** — URL ID extraction, CSV parsing, weighted-random card selection, localStorage deck cache. The Go side is a thin server: embeds `index.html` via `//go:embed` and provides a `/csv` CORS proxy for the browser. In production, `/csv` is re-implemented as a Netlify Function (`netlify/functions/csv.js`) and routed via `netlify.toml`. When changing flashcards behaviour, the interesting code is almost always in `index.html`, not `main.go`.

### sheetcast (`cmd/sheetcast/`)

Go CLI. Synthesises the target-language column via Google Cloud TTS, concatenates clips into one WAV/MP3 with configurable silence. Crucially, **sheetcast does not share Go code with flashcards** — flashcards' sheet-handling is all JS, so `internal/sheets` was written fresh for the CLI. Auth is Application Default Credentials; billing flows to the caller's GCP project.

### Data flow (sheetcast)

1. `internal/sheets.ExtractID` — parses the sharing URL or accepts a raw ID.
2. `internal/sheets.FetchCSV` — unauthenticated GET to gviz. **Private sheets return a 200 with `Content-Type: text/html` (a login page), not 401/403** — detection must check content-type, not just status.
3. `internal/sheets.ParseColumn` — stdlib `encoding/csv`; column spec accepts letter, 1-based number, or `#Header` lookup.
4. `internal/sheets.HeaderOf` + `internal/tts.VoiceFromLangCode` — if column header looks like a 2-letter ISO-639-1 code (e.g. `IT`), the CLI auto-selects a matching Neural2 voice.
5. `internal/tts.Synthesize` — one request per sentence, bounded concurrency in the caller. Transient errors (`ResourceExhausted`, `Unavailable`, `DeadlineExceeded`) are retried with exponential backoff; everything else fails the whole run (no partial output).
6. `internal/audio.StripWAVHeader` — parses RIFF chunks. **Do not shortcut with a 44-byte skip: Google TTS responses sometimes contain a `LIST` info chunk between `fmt ` and `data`.** Clicks at clip boundaries mean this walker is wrong.
7. `internal/audio.WriteWAV` — raw PCM concat + canonical 44-byte RIFF header. Silence is zero bytes, sample-aligned to `blockAlign`.
8. `internal/audio.EncodeMP3` — optional shell-out to `ffmpeg` (preflighted via `LookPath` before any TTS call so `--mp3` without ffmpeg fails fast).

All clips must share `Format` (sample rate / channels / bit depth). `tts.Client` pins `AudioConfig` at construction to enforce this.

## Conventions

- **`cmd/` is for CLI tools, `app/` for web apps.** This was established by commit `2a62ac3` (moved flashcards out of `cmd/`).
- **`internal/` packages have no external consumers.** sheets, audio, and tts are split by responsibility, not by sharing pressure — they could be merged if it ever makes sense.
- **CLI stack:** `github.com/jessevdk/go-flags` for parsing (struct tags, env support, built-in choice validation); `log/slog` + `github.com/lmittmann/tint` for stderr output. `Verbose []bool` is a counter: `-v` → `slog.LevelInfo`, `-vv` → `slog.LevelDebug`. Do not reach for stdlib `flag` or `fmt.Fprintf(os.Stderr, ...)` in new code.
- **Tests don't hit the network.** `internal/sheets` tests use inline CSV fixtures; HTTP-response classification is extracted into an unexported helper (`interpretCSVResponse`) so tests don't need an httptest server. TTS is covered only at the pure-helper level (`LanguageFromVoice`, `VoiceFromLangCode`) — there's no mocking of the Google client.
- **Binary size:** sheetcast is ~24MB. The Google TTS SDK pulls in gRPC + OTel + protobuf (~30 transitive modules). This is accepted — there is no meaningfully lighter first-party path. flashcards' binary is ~9MB.

## Non-obvious gotchas

- The gviz endpoint pads each row to 26 columns with empty cells, so out-of-range errors only trigger for columns beyond AA. Column Z on a 2-column sheet returns `ErrEmptyColumn`, not a range error — this is acceptable UX.
- `go mod tidy` will reshuffle `go.mod` because several deps (`google.golang.org/api`, `tint`) are used directly despite go-get adding them as indirect. Run tidy before committing dep changes.
- `.envrc.local` is committed; `.envrc` is not. Put anything sensitive or machine-specific in `.envrc`, anything shareable in `.envrc.local`.
