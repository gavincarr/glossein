package tts

import (
	"fmt"
	"strings"
)

// LanguageFromVoice parses a Google TTS voice name and returns the lang-region
// prefix. Examples:
//
//	"en-US-Neural2-D"       → "en-US"
//	"it-IT-Wavenet-A"       → "it-IT"
//	"en-US-Chirp-HD-Charon" → "en-US"
//
// Falls back to "en-US" for unrecognised shapes.
func LanguageFromVoice(voice string) string {
	parts := strings.Split(voice, "-")
	if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
		return parts[0] + "-" + parts[1]
	}
	return "en-US"
}

// langCodeVoices maps 2-letter ISO 639-1 codes to a reasonable default
// Neural2 voice. Only listed when the region differs from an uppercase
// duplicate of the language code (e.g. en → en-US, not en-EN).
var langCodeVoices = map[string]string{
	"en": "en-US-Neural2-D",
	"pt": "pt-PT-Neural2-A",
	"zh": "cmn-CN-Wavenet-A", // zh rarely has Neural2; Wavenet is ubiquitous
	"ja": "ja-JP-Neural2-B",
	"ko": "ko-KR-Neural2-A",
	"ar": "ar-XA-Wavenet-A",
}

// VoiceFromLangCode maps a 2-letter language code (e.g. "IT", "it", "fr")
// to a reasonable Neural2 voice. Returns ("", false) if the input isn't a
// plausible language code.
func VoiceFromLangCode(code string) (string, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	if len(code) != 2 {
		return "", false
	}
	for _, c := range code {
		if c < 'a' || c > 'z' {
			return "", false
		}
	}
	if v, ok := langCodeVoices[code]; ok {
		return v, true
	}
	return fmt.Sprintf("%s-%s-Neural2-A", code, strings.ToUpper(code)), true
}
