// Package embed turns text into dense vectors. Three backends are supported: a
// zero-dependency hash embedder that always works offline, a local Ollama
// server, and any OpenAI-compatible embeddings API.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"llm-memory/internal/config"
)

// Embedder converts a batch of texts into vectors of a fixed dimension.
type Embedder interface {
	// Name identifies the backend and model. It is stored with the database so
	// a change can be detected.
	Name() string
	// Embed returns one vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// New resolves the configured embedder. "auto" probes Ollama and falls back to
// the built-in hash embedder when no local model is reachable.
func New(ctx context.Context, cfg config.Config) (Embedder, error) {
	switch cfg.Embedder {
	case "hash":
		return NewHash(256), nil
	case "ollama":
		return NewOllama(cfg.OllamaURL, cfg.EmbedModel), nil
	case "openai":
		if cfg.EmbedURL == "" || cfg.EmbedModel == "" {
			return nil, fmt.Errorf("embedder openai needs embed_url and embed_model")
		}
		return NewOpenAI(cfg.EmbedURL, cfg.EmbedKey, cfg.EmbedModel), nil
	case "auto", "":
		o := NewOllama(cfg.OllamaURL, cfg.EmbedModel)
		probe, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if o.Available(probe) {
			return o, nil
		}
		return NewHash(256), nil
	default:
		return nil, fmt.Errorf("unknown embedder %q (use auto, hash, ollama, or openai)", cfg.Embedder)
	}
}

// Tokenize lowercases text and splits it into alphanumeric tokens of length two
// or more. It is shared by the hash embedder and the keyword search builder.
func Tokenize(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) >= 2 {
			out = append(out, f)
		}
	}
	return out
}

// httpClient is the shared client for embedding and extraction calls.
var httpClient = &http.Client{Timeout: 60 * time.Second}

// postJSON sends body to url and decodes the JSON response into out.
func postJSON(ctx context.Context, url string, headers map[string]string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s: %s: %s", url, resp.Status, truncate(string(payload), 300))
	}
	return json.Unmarshal(payload, out)
}

// getJSON fetches url and decodes the JSON response into out.
func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
