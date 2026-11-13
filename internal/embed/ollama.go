package embed

import (
	"context"
	"fmt"
	"strings"
)

// Ollama embeds through a local Ollama server.
type Ollama struct {
	base  string
	model string
}

// NewOllama builds an Ollama embedder.
func NewOllama(base, model string) *Ollama {
	if model == "" {
		model = "nomic-embed-text"
	}
	return &Ollama{base: base, model: model}
}

// Name implements Embedder.
func (o *Ollama) Name() string { return "ollama:" + o.model }

// Available reports whether the server is reachable and the model is pulled. It
// is a cheap GET /api/tags, so auto-detection does not pay for an embedding.
func (o *Ollama) Available(ctx context.Context) bool {
	var tags struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := getJSON(ctx, o.base+"/api/tags", &tags); err != nil {
		return false
	}
	want := o.model
	for _, m := range tags.Models {
		name := m.Name
		if name == "" {
			name = m.Model
		}
		if name == want || strings.TrimSuffix(name, ":latest") == strings.TrimSuffix(want, ":latest") {
			return true
		}
	}
	return false
}

// Embed implements Embedder using the batch endpoint, falling back to the
// single-prompt endpoint for older servers.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	err := postJSON(ctx, o.base+"/api/embed", nil, map[string]any{
		"model": o.model,
		"input": texts,
	}, &resp)
	if err == nil && len(resp.Embeddings) == len(texts) {
		out := make([][]float32, len(texts))
		for i, vec := range resp.Embeddings {
			out[i] = narrow(vec)
		}
		return out, nil
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		var one struct {
			Embedding []float64 `json:"embedding"`
		}
		if err := postJSON(ctx, o.base+"/api/embeddings", nil, map[string]any{
			"model":  o.model,
			"prompt": text,
		}, &one); err != nil {
			return nil, fmt.Errorf("ollama embed: %w", err)
		}
		if len(one.Embedding) == 0 {
			return nil, fmt.Errorf("ollama embed: empty vector for %q", truncate(text, 40))
		}
		out[i] = narrow(one.Embedding)
	}
	return out, nil
}

// narrow converts a float64 vector to float32.
func narrow(vec []float64) []float32 {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float32(v)
	}
	return out
}
