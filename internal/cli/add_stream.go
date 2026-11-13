package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"llm-memory/internal/memory"
)

// maxJSONLLine caps one line of streamed input. Memories are small; this only
// protects against accidentally piping in something that is not JSONL.
const maxJSONLLine = 16 << 20

// jsonlRecord is one memory in a JSONL stream. Optional fields override the
// command-line defaults; absent fields keep them.
type jsonlRecord struct {
	Content    string    `json:"content"`
	Text       *string   `json:"text"`
	Category   *string   `json:"category"`
	Importance *float64  `json:"importance"`
	Tags       *[]string `json:"tags"`
	Source     *string   `json:"source"`
	Session    *string   `json:"session"`
}

// parseJSONLRecord merges one JSONL line with the flag defaults.
func parseJSONLRecord(line []byte, defaults memory.AddInput) (memory.AddInput, error) {
	var rec jsonlRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return memory.AddInput{}, err
	}
	content := rec.Content
	if strings.TrimSpace(content) == "" && rec.Text != nil {
		content = *rec.Text
	}
	if strings.TrimSpace(content) == "" {
		return memory.AddInput{}, errors.New("memory content is empty")
	}

	in := defaults
	in.Content = content
	if rec.Category != nil {
		in.Category = *rec.Category
	}
	if rec.Importance != nil {
		in.Importance = *rec.Importance
	}
	if rec.Tags != nil {
		in.Tags = append([]string{}, (*rec.Tags)...)
	}
	if rec.Source != nil {
		in.Source = *rec.Source
	}
	if rec.Session != nil {
		in.Session = *rec.Session
	}
	return in, nil
}

// scanAddInputs reads stdin and calls fn for every memory. A line that starts
// with "{" switches to JSONL mode; anything else is stored as one plain-text
// memory, which preserves the original behavior of "add -".
func scanAddInputs(r io.Reader, defaults memory.AddInput, forceJSONL bool, fn func(memory.AddInput) error) (bool, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), maxJSONLLine)

	var first string
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if line := scanner.Text(); strings.TrimSpace(line) != "" {
			first = line
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	if first == "" {
		if forceJSONL {
			return true, nil
		}
		in := defaults
		in.Content = ""
		return false, fn(in)
	}

	jsonl := forceJSONL || strings.HasPrefix(strings.TrimSpace(first), "{")
	if !jsonl {
		var sb strings.Builder
		sb.WriteString(first)
		for scanner.Scan() {
			sb.WriteString("\n")
			sb.WriteString(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return false, err
		}
		in := defaults
		in.Content = sb.String()
		return false, fn(in)
	}

	emit := func(line string, n int) error {
		in, err := parseJSONLRecord([]byte(line), defaults)
		if err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		return fn(in)
	}
	if err := emit(first, lineNo); err != nil {
		return true, err
	}
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := emit(line, lineNo); err != nil {
			return true, err
		}
	}
	return true, scanner.Err()
}

// runAddStdin stores stdin as either one memory or a JSONL stream, opening the
// service only once for the whole stream.
func runAddStdin(cmd *cobra.Command, a *App, defaults memory.AddInput, forceJSONL bool) error {
	svc, err := a.service(cmd.Context())
	if err != nil {
		return err
	}
	defer svc.Close()

	var results []*memory.AddResult
	stream, err := scanAddInputs(os.Stdin, defaults, forceJSONL, func(in memory.AddInput) error {
		res, err := svc.Add(cmd.Context(), in)
		if err != nil {
			return err
		}
		results = append(results, res)
		if !a.json {
			printAddResult(res)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return errors.New("no memories in input")
	}
	if !a.json {
		return nil
	}
	if !stream {
		return a.printJSON(addDTO(results[0]))
	}
	out := make([]addJSON, 0, len(results))
	for _, res := range results {
		out = append(out, addDTO(res))
	}
	return a.printJSON(out)
}
