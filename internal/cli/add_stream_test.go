package cli

import (
	"strings"
	"testing"

	"llm-memory/internal/memory"
)

func TestScanAddInputsJSONL(t *testing.T) {
	defaults := memory.AddInput{
		Category: "fact", Importance: 0.5,
		Tags: []string{"base"}, Source: "default-source",
	}
	input := strings.NewReader(`
{"content":"first"}
{"text":"second","category":"decision","importance":0.9,"tags":["x"],"source":""}
`)
	var got []memory.AddInput
	stream, err := scanAddInputs(input, defaults, false, func(in memory.AddInput) error {
		got = append(got, in)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stream {
		t.Fatal("expected JSONL mode")
	}
	if len(got) != 2 {
		t.Fatalf("got %d memories, want 2", len(got))
	}
	if got[0].Content != "first" || got[0].Category != "fact" {
		t.Fatalf("defaults were not applied: %+v", got[0])
	}
	if len(got[0].Tags) != 1 || got[0].Tags[0] != "base" || got[0].Source != "default-source" {
		t.Fatalf("default tags/source were not applied: %+v", got[0])
	}
	if got[1].Content != "second" || got[1].Category != "decision" || got[1].Importance != 0.9 {
		t.Fatalf("line fields did not override defaults: %+v", got[1])
	}
	if len(got[1].Tags) != 1 || got[1].Tags[0] != "x" || got[1].Source != "" {
		t.Fatalf("line tags/source did not override defaults: %+v", got[1])
	}
}

func TestScanAddInputsPlainText(t *testing.T) {
	input := strings.NewReader("line one\nline two\n")
	var got []memory.AddInput
	stream, err := scanAddInputs(input, memory.AddInput{}, false, func(in memory.AddInput) error {
		got = append(got, in)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if stream {
		t.Fatal("plain text must not be reported as JSONL")
	}
	if len(got) != 1 || got[0].Content != "line one\nline two" {
		t.Fatalf("unexpected plain-text input: %+v", got)
	}
}

func TestScanAddInputsInvalidJSONL(t *testing.T) {
	input := strings.NewReader("{\"content\":\"ok\"}\nnot json\n")
	var got []memory.AddInput
	_, err := scanAddInputs(input, memory.AddInput{}, false, func(in memory.AddInput) error {
		got = append(got, in)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("expected a line 2 error, got %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("valid lines before the error should be stored, got %d", len(got))
	}
}

func TestScanAddInputsEmptyJSONL(t *testing.T) {
	var got []memory.AddInput
	stream, err := scanAddInputs(strings.NewReader("\n\n"), memory.AddInput{}, true, func(in memory.AddInput) error {
		got = append(got, in)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !stream || len(got) != 0 {
		t.Fatalf("unexpected result: stream=%v inputs=%d", stream, len(got))
	}
}
