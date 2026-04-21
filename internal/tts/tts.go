// Package tts wraps Google Cloud Text-to-Speech for sheetcast.
//
// Authentication is Application Default Credentials — users run
// `gcloud auth application-default login` once. Billing flows to the
// user's own GCP project.
package tts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	texttospeech "cloud.google.com/go/texttospeech/apiv1"
	"cloud.google.com/go/texttospeech/apiv1/texttospeechpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Client synthesises plain text to LINEAR16 WAV audio via a single voice.
type Client struct {
	raw    *texttospeech.Client
	voice  *texttospeechpb.VoiceSelectionParams
	config *texttospeechpb.AudioConfig
}

// New creates a Client for the given voice (e.g. "en-US-Neural2-D") and sample
// rate. If lang is empty, the language code is inferred from the voice name
// ("en-US-Neural2-D" → "en-US"). Uses Application Default Credentials.
func New(ctx context.Context, voice, lang string, sampleRate int) (*Client, error) {
	if lang == "" {
		lang = LanguageFromVoice(voice)
	}
	raw, err := texttospeech.NewClient(ctx)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "could not find default credentials") || strings.Contains(msg, "google: could not find") {
			return nil, errors.New("Google credentials not found. Run 'gcloud auth application-default login' (or set GOOGLE_APPLICATION_CREDENTIALS to a service-account JSON), then retry")
		}
		return nil, fmt.Errorf("initialising TTS client: %w", err)
	}
	return &Client{
		raw: raw,
		voice: &texttospeechpb.VoiceSelectionParams{
			LanguageCode: lang,
			Name:         voice,
		},
		config: &texttospeechpb.AudioConfig{
			AudioEncoding:   texttospeechpb.AudioEncoding_LINEAR16,
			SampleRateHertz: int32(sampleRate),
		},
	}, nil
}

// Synthesize returns the full WAV bytes (with RIFF header) for text.
// Retries transient errors with exponential backoff (500ms / 1s / 2s).
func (c *Client) Synthesize(ctx context.Context, text string) ([]byte, error) {
	req := &texttospeechpb.SynthesizeSpeechRequest{
		Input: &texttospeechpb.SynthesisInput{
			InputSource: &texttospeechpb.SynthesisInput_Text{Text: text},
		},
		Voice:       c.voice,
		AudioConfig: c.config,
	}
	backoffs := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	var lastErr error
	for attempt := 0; attempt <= len(backoffs); attempt++ {
		resp, err := c.raw.SynthesizeSpeech(ctx, req)
		if err == nil {
			return resp.AudioContent, nil
		}
		lastErr = err
		if !isTransient(err) || attempt == len(backoffs) {
			break
		}
		select {
		case <-time.After(backoffs[attempt]):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("synthesising %q: %w", truncate(text, 60), lastErr)
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}
	return c.raw.Close()
}

func isTransient(err error) bool {
	s, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch s.Code() {
	case codes.ResourceExhausted, codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
