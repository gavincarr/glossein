package tts

import "testing"

func TestLanguageFromVoice(t *testing.T) {
	cases := map[string]string{
		"en-US-Neural2-D":       "en-US",
		"it-IT-Wavenet-A":       "it-IT",
		"en-US-Chirp-HD-Charon": "en-US",
		"ja-JP-Standard-A":      "ja-JP",
		"":                      "en-US",
		"nonsense":              "en-US",
	}
	for in, want := range cases {
		if got := LanguageFromVoice(in); got != want {
			t.Errorf("LanguageFromVoice(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVoiceFromLangCode(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantOK  bool
	}{
		{"IT", "it-IT-Neural2-A", true},
		{"it", "it-IT-Neural2-A", true},
		{" FR ", "fr-FR-Neural2-A", true},
		{"de", "de-DE-Neural2-A", true},
		{"EN", "en-US-Neural2-D", true}, // overridden region (en-EN doesn't exist)
		{"pt", "pt-PT-Neural2-A", true},
		{"", "", false},
		{"eng", "", false},
		{"en-US", "", false},
		{"12", "", false},
		{"i t", "", false},
	}
	for _, tc := range cases {
		got, ok := VoiceFromLangCode(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("VoiceFromLangCode(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
