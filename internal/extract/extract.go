// Package extract turns raw session text into atomic memories with an
// OpenAI-compatible chat model. It is optional: without configuration the CLI
// falls back to paragraph chunking.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fact is one atomic memory proposed by the model.
type Fact struct {
	Content    string   `json:"content"`
	Category   string   `json:"category"`
	Importance float64  `json:"importance"`
	Tags       []string `json:"tags"`
}

// Extractor calls a chat completions endpoint.
type Extractor struct {
	url   string
	key   string
	model string
	http  *http.Client
}

// New builds an extractor for an OpenAI-compatible base URL, for example
// http://localhost:11434/v1 or https://api.openai.com/v1.
func New(url, key, model string) *Extractor {
	return &Extractor{
		url:   strings.TrimRight(url, "/"),
		key:   key,
		model: model,
		http:  &http.Client{Timeout: 120 * time.Second},
	}
}

const systemPrompt = `You extract durable memories from text for a long-term memory store.

Return ONLY JSON of the form {"facts":[{"content":"...","category":"fact","importance":0.5,"tags":["..."]}]}.

Rules:
- Split the text into atomic, self-contained statements. Each fact must be understandable on its own.
- category is one of: fact, finding, decision, event, task, insight, preference.
- importance is 0.0 to 1.0. Decisions, preferences, and explicit commitments score high; incidental details score low.
- tags are 1-4 short lowercase keywords.
- Keep names, numbers, versions, paths, and dates exactly as written.
- Do not invent facts. Do not add commentary.`

// Extract asks the model for atomic facts. The returned content is trimmed and
// facts with empty content are dropped.
func (e *Extractor) Extract(ctx context.Context, text string) ([]Fact, error) {
	body := map[string]any{
		"model": e.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": text},
		},
		"temperature": 0.1,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("extract: %s: %s", resp.Status, truncate(string(payload), 300))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(payload, &completion); err != nil {
		return nil, fmt.Errorf("extract: decode response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("extract: no choices in response")
	}
	content := stripFence(completion.Choices[0].Message.Content)
	var parsed struct {
		Facts []Fact `json:"facts"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("extract: model did not return valid JSON: %w (got %q)", err, truncate(content, 200))
	}
	out := make([]Fact, 0, len(parsed.Facts))
	for _, f := range parsed.Facts {
		f.Content = strings.TrimSpace(f.Content)
		if f.Content == "" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// stripFence removes a Markdown code fence around JSON.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
