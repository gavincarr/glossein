# glossein

Personal language-learning tools built around the idea that a simple
two-column Google Sheet (native language / target language) is a great
unit for vocabulary content — easy to edit, easy to share, easy to
version. The same sheet can drive multiple study modes.

## Sheet format

All tools expect:

- A two-column Google Sheet shared as **Share → Anyone with the link → Viewer**.

- Row 1 is a header. For flashcards it's skipped; for sheetcast it's
  used (optionally) to auto-select a TTS voice if the header title is
  a language code (e.g. "EN", "IT", "FR", "CN", etc.)

- Column A is the prompt / source language; Column B is the answer /
  target language. sheetcast defaults to reading column B and can be
  pointed elsewhere via `--column`.

A template sheet is available:
[Glossein Template](https://docs.google.com/spreadsheets/d/12TWGqpozKTFMBuqE96bUbZIYDtL__VeEOVK87NEERLE/edit?usp=sharing).
Use **File > Make a copy** and then customise for personal use
(remembering to reshare the copy so the tools can read it).

## Tools

### flashcards

A static web app, deployed at **https://glossein.netlify.app**, that
takes a publicly-shared Google Sheets URL and turns it into a weighted-
random flashcard trainer. Cards you miss come back more often; cards
you get right fade out. Recent decks and per-deck state are kept in
browser `localStorage`.

Or you can run locally (it's a small Go server wrapping embedded HTML):

```
go run ./app/flashcards -port 8080
```

### sheetcast

A Go CLI that takes the same sheet URLs and produces a single audio
file — all target-language sentences synthesised via Google Cloud TTS
and concatenated with configurable silence between them. Good for
passive listening on a commute, shadowing, or drilling active recall
with longer gaps.

Install or build:

```
# install onto your PATH
go install github.com/gavincarr/glossein/cmd/sheetcast@latest

# or build a local binary from a checkout
go build -o sheetcast ./cmd/sheetcast
```

sheetcast uses Application Default Credentials — billing and quota flow
directly to your own GCP project. Pick one of the two one-time auth
setups below.

**Option A — `gcloud` (fastest if you already have it and are happy to set globally):**

```
gcloud auth application-default login
gcloud services enable texttospeech.googleapis.com
gcloud auth application-default set-quota-project YOUR_PROJECT
```

**Option B — scoped service-account JSON key (no `gcloud` required):**

1. In the GCP console, create (or pick) a project and enable the
   **Cloud Text-to-Speech API**.
2. Under **IAM & Admin → Service Accounts**, create a service account,
   grant it a role that allows TTS calls (`roles/editor` is the easiest;
   tighten to a custom role with `texttospeech.*` permissions if you prefer),
   then create and download a JSON key.
3. Export the path and project (in your `~/.bashrc` if global use is okay;
   or in a `.envrc` file somewhere if you're using `direnv`):

```
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/key.json
export GOOGLE_CLOUD_PROJECT=YOUR_PROJECT
```

Then run it:

```
# default: listen-mode WAV
./sheetcast <sheet-url-or-id>

# shadowing mode, MP3 output, explicit project
./sheetcast --mode shadow --mp3 -o italian.mp3 --project myproj <url>
```

The voice is auto-detected from the column header — a cell like `IT`
in row 1 selects `it-IT-Neural2-A`; `--voice` overrides it. See
`./sheetcast --help` for the full flag set.

## Repo layout

```
app/flashcards/     web app (Go server + embedded HTML/JS)
cmd/sheetcast/      Go CLI
internal/sheets/    Google Sheets ID + CSV fetch/parse
internal/audio/     WAV concatenation + MP3 transcode
internal/tts/       Google Cloud TTS wrapper
```
