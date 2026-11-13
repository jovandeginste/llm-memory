// Package config resolves the llmmem configuration from a JSON file and the
// environment. Every value has a working default, so the tool runs with no
// configuration at all.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the on-disk configuration. Paths may start with "~".
type Config struct {
	// DB is the SQLite file that holds memories, links, and metadata.
	DB string `json:"db"`

	// Embedder selects how text is turned into vectors: auto, hash, ollama, or
	// openai. auto probes Ollama and falls back to the built-in hash embedder.
	Embedder string `json:"embedder"`

	OllamaURL  string `json:"ollama_url"`
	EmbedModel string `json:"embed_model"`
	EmbedURL   string `json:"embed_url"`
	EmbedKey   string `json:"embed_api_key"`

	// LinkThreshold is the cosine similarity above which a new memory is linked
	// to an existing one. LinkTopK caps how many links a new memory creates.
	LinkThreshold float64 `json:"link_threshold"`
	LinkTopK      int     `json:"link_topk"`

	// Optional LLM used by "llmmem ingest" to split raw text into atomic
	// memories. Any OpenAI-compatible chat endpoint works.
	ExtractURL   string `json:"extract_url"`
	ExtractModel string `json:"extract_model"`
	ExtractKey   string `json:"extract_api_key"`
}

// Defaults returns the baseline configuration.
func Defaults() Config {
	return Config{
		DB:            filepath.Join(DataDir(), "memory.db"),
		Embedder:      "auto",
		OllamaURL:     "http://localhost:11434",
		EmbedModel:    "nomic-embed-text",
		EmbedURL:      "https://api.openai.com/v1",
		LinkThreshold: 0.5,
		LinkTopK:      5,
	}
}

// ConfigPath returns the configuration file location.
func ConfigPath() string {
	if p := os.Getenv("LLM_MEMORY_CONFIG"); p != "" {
		return Expand(p)
	}
	return filepath.Join(configDir(), "config.json")
}

// DataDir returns the default directory for the database.
func DataDir() string {
	if p := os.Getenv("LLM_MEMORY_DATA"); p != "" {
		return Expand(p)
	}
	return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "llm-memory")
}

// configDir returns the directory holding config.json.
func configDir() string {
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "llm-memory")
}

// xdg resolves an XDG base directory.
func xdg(env, fallback string) string {
	if p := os.Getenv(env); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fallback
	}
	return filepath.Join(home, fallback)
}

// Load reads path if it exists and layers the environment on top. A missing
// file is not an error: the defaults are used.
func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(data, &cfg); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		case errors.Is(err, fs.ErrNotExist):
			// defaults only
		default:
			return cfg, fmt.Errorf("read %s: %w", path, err)
		}
	}
	cfg.applyEnv()
	cfg.normalize()
	return cfg, nil
}

// applyEnv overrides configuration with LLM_MEMORY_* variables.
func (c *Config) applyEnv() {
	set := func(dst *string, key string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	set(&c.DB, "LLM_MEMORY_DB")
	set(&c.Embedder, "LLM_MEMORY_EMBEDDER")
	set(&c.OllamaURL, "LLM_MEMORY_OLLAMA_URL")
	set(&c.EmbedModel, "LLM_MEMORY_EMBED_MODEL")
	set(&c.EmbedURL, "LLM_MEMORY_EMBED_URL")
	set(&c.EmbedKey, "LLM_MEMORY_EMBED_API_KEY")
	set(&c.ExtractURL, "LLM_MEMORY_EXTRACT_URL")
	set(&c.ExtractModel, "LLM_MEMORY_EXTRACT_MODEL")
	set(&c.ExtractKey, "LLM_MEMORY_EXTRACT_API_KEY")
	if v := os.Getenv("LLM_MEMORY_LINK_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.LinkThreshold = f
		}
	}
	if v := os.Getenv("LLM_MEMORY_LINK_TOPK"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.LinkTopK = n
		}
	}
}

// normalize expands paths and fixes out-of-range values.
func (c *Config) normalize() {
	c.DB = Expand(c.DB)
	c.Embedder = strings.ToLower(strings.TrimSpace(c.Embedder))
	if c.Embedder == "" {
		c.Embedder = "auto"
	}
	if c.LinkThreshold <= 0 || c.LinkThreshold > 1 {
		c.LinkThreshold = 0.5
	}
	if c.LinkTopK <= 0 {
		c.LinkTopK = 5
	}
	c.OllamaURL = strings.TrimRight(c.OllamaURL, "/")
	c.EmbedURL = strings.TrimRight(c.EmbedURL, "/")
	c.ExtractURL = strings.TrimRight(c.ExtractURL, "/")
}

// Save writes the configuration as indented JSON.
func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Expand resolves a leading "~" to the user's home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}
