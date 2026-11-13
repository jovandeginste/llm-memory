package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingUsesDefaults(t *testing.T) {
	t.Setenv("LLM_MEMORY_CONFIG", "")
	t.Setenv("LLM_MEMORY_DB", "")
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embedder != "auto" || cfg.LinkThreshold != 0.5 || cfg.LinkTopK != 5 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DB == "" {
		t.Fatal("default database path is empty")
	}
}

func TestEnvOverrides(t *testing.T) {
	t.Setenv("LLM_MEMORY_DB", "/tmp/env-memory.db")
	t.Setenv("LLM_MEMORY_EMBEDDER", "hash")
	t.Setenv("LLM_MEMORY_LINK_THRESHOLD", "0.72")
	t.Setenv("LLM_MEMORY_LINK_TOPK", "9")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB != "/tmp/env-memory.db" || cfg.Embedder != "hash" {
		t.Fatalf("env not applied: %+v", cfg)
	}
	if cfg.LinkThreshold != 0.72 || cfg.LinkTopK != 9 {
		t.Fatalf("numeric env not applied: %+v", cfg)
	}
}

func TestNormalizeClamps(t *testing.T) {
	c := Config{Embedder: "  HASH ", LinkThreshold: 7, LinkTopK: -1}
	c.normalize()
	if c.Embedder != "hash" {
		t.Fatalf("embedder = %q", c.Embedder)
	}
	if c.LinkThreshold != 0.5 || c.LinkTopK != 5 {
		t.Fatalf("clamping failed: %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("LLM_MEMORY_DB", "")
	path := filepath.Join(t.TempDir(), "config.json")
	want := Defaults()
	want.DB = "/tmp/custom.db"
	want.Embedder = "ollama"
	want.LinkThreshold = 0.6
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DB != want.DB || got.Embedder != want.Embedder || got.LinkThreshold != want.LinkThreshold {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestExpand(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := Expand("~/memory.db"); got != filepath.Join(home, "memory.db") {
		t.Fatalf("Expand = %q", got)
	}
	if got := Expand("/absolute/path"); got != "/absolute/path" {
		t.Fatalf("Expand = %q", got)
	}
}
