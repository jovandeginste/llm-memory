package embed

import (
	"context"
	"fmt"
)

// OpenAI embeds through any OpenAI-compatible /embeddings endpoint.
type OpenAI struct {
	base  string
	key   string
	model string
}

// NewOpenAI builds an OpenAI-compatible embedder.
func NewOpenAI(base, key, model string) *OpenAI {
	return &OpenAI{base: base, key: key, model: model}
}

// Name implements Embedder.
func (o *OpenAI) Name() string { return "openai:" + o.model }

// Embed implements Embedder.
func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var resp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	headers := map[string]string{}
	if o.key != "" {
		headers["Authorization"] = "Bearer " + o.key
	}
	err := postJSON(ctx, o.base+"/embeddings", headers, map[string]any{
		"model": o.model,
		"input": texts,
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("openai embed: %w", err)
	}
	if len(resp.Data) != len(texts) {
		return nil, fmt.Errorf("openai embed: got %d vectors for %d inputs", len(resp.Data), len(texts))
	}
	out := make([][]float32, len(texts))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(out) {
			return nil, fmt.Errorf("openai embed: bad index %d", d.Index)
		}
		out[d.Index] = narrow(d.Embedding)
	}
	return out, nil
}
