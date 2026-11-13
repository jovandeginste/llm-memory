// Package cli implements the llmmem command line interface with Cobra.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"llm-memory/internal/config"
	"llm-memory/internal/memory"
	"llm-memory/internal/store"
	"llm-memory/internal/version"
)

// Version is the tool version.
const Version = version.Version

// App carries the resolved configuration for one invocation.
type App struct {
	cfg     config.Config
	cfgPath string
	json    bool
}

// Run builds the command tree and executes it.
func Run(args []string) error {
	a := &App{}
	root := newRootCmd(a)
	root.SetArgs(args)
	return root.Execute()
}

// newRootCmd wires the root command and its persistent flags.
func newRootCmd(a *App) *cobra.Command {
	var cfgPath, dbPath string
	var jsonOut bool

	root := &cobra.Command{
		Use:   "llmmem",
		Short: "Persistent, self-linking memory for LLM agents",
		Long: `llmmem stores memories in SQLite, embeds them, and links each new memory
to the ones it resembles. Search fuses vector and keyword retrieval and can
expand over the link graph.`,
		Example: `  llmmem add "We prefer Go for CLI tools" --category preference --tags go,cli
  llmmem search "language preference" --k 5
  llmmem context --days 7 --write CONTEXT.md
  llmmem decay --dry-run`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				// init must be able to replace a broken configuration.
				if cmd.Name() != "init" {
					return err
				}
				cfg = config.Defaults()
			}
			if dbPath != "" {
				cfg.DB = config.Expand(dbPath)
			}
			a.cfg = cfg
			a.cfgPath = cfgPath
			a.json = jsonOut
			return nil
		},
	}
	root.SetVersionTemplate("llmmem {{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true

	pf := root.PersistentFlags()
	pf.StringVar(&cfgPath, "config", config.ConfigPath(), "configuration file")
	pf.StringVar(&dbPath, "db", "", "database file (overrides config)")
	pf.BoolVar(&jsonOut, "json", false, "machine-readable JSON output")

	root.AddCommand(
		newInitCmd(a),
		newAddCmd(a),
		newIngestCmd(a),
		newSearchCmd(a),
		newGetCmd(a),
		newListCmd(a),
		newUpdateCmd(a),
		newDeleteCmd(a),
		newRestoreCmd(a),
		newReinforceCmd(a),
		newLinkCmd(a),
		newUnlinkCmd(a),
		newLinksCmd(a),
		newGraphCmd(a),
		newContextCmd(a),
		newDecayCmd(a),
		newStatsCmd(a),
		newReindexCmd(a),
		newConfigCmd(a),
		newEmbedCmd(a),
		newMCPCmd(a),
		newVersionCmd(a),
	)
	return root
}

// service opens the memory service for one command.
func (a *App) service(ctx context.Context) (*memory.Service, error) {
	return memory.Open(ctx, a.cfg)
}

// printJSON writes v as indented JSON.
func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// memoryJSON is the stable JSON shape for a memory.
type memoryJSON struct {
	ID          int64    `json:"id"`
	Category    string   `json:"category"`
	Content     string   `json:"content"`
	Importance  float64  `json:"importance"`
	Tags        []string `json:"tags,omitempty"`
	Source      string   `json:"source,omitempty"`
	Session     string   `json:"session,omitempty"`
	Strength    float64  `json:"strength"`
	Links       int      `json:"links"`
	AccessCount int      `json:"access_count"`
	Archived    bool     `json:"archived,omitempty"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	LastAccess  string   `json:"last_access"`
}

// memoryDTO converts a stored memory to its JSON shape.
func memoryDTO(m *store.Memory) memoryJSON {
	return memoryJSON{
		ID: m.ID, Category: m.Category, Content: m.Content,
		Importance: m.Importance, Tags: m.Tags, Source: m.Source, Session: m.Session,
		Strength: m.Strength, Links: m.Degree, AccessCount: m.Access, Archived: m.Archived,
		CreatedAt:  m.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  m.UpdatedAt.Format(time.RFC3339),
		LastAccess: m.LastAccess.Format(time.RFC3339),
	}
}

// linkJSON is the JSON shape of a link.
type linkJSON struct {
	ID         int64   `json:"id"`
	Similarity float64 `json:"similarity"`
	Content    string  `json:"content,omitempty"`
	Category   string  `json:"category,omitempty"`
}

// addJSON is a stored memory plus the links that were created for it.
type addJSON struct {
	memoryJSON
	Linked []linkJSON `json:"linked"`
}

// addDTO converts an add result to its JSON shape.
func addDTO(res *memory.AddResult) addJSON {
	out := addJSON{memoryJSON: memoryDTO(res.Memory), Linked: []linkJSON{}}
	for _, l := range res.Links {
		out.Linked = append(out.Linked, linkJSON{ID: l.Dst, Similarity: l.Similarity})
	}
	return out
}

// detailJSON is a memory with its links.
type detailJSON struct {
	memoryJSON
	Linked []linkJSON `json:"linked"`
}

// detailDTO converts a memory detail to its JSON shape.
func detailDTO(d *memory.Detail) detailJSON {
	out := detailJSON{memoryJSON: memoryDTO(d.Memory), Linked: []linkJSON{}}
	for _, l := range d.Links {
		out.Linked = append(out.Linked, linkJSON{
			ID: l.Link.Dst, Similarity: l.Link.Similarity,
			Content: l.Other.Content, Category: l.Other.Category,
		})
	}
	return out
}

// memoryLine renders a memory in the human-readable form.
func memoryLine(w io.Writer, m *store.Memory, prefix string) {
	fmt.Fprintf(w, "%s#%d [%s] strength=%.2f links=%d\n",
		prefix, m.ID, m.Category, m.Strength, m.Degree)
	fmt.Fprintf(w, "    %s\n", strings.Join(strings.Fields(m.Content), " "))
	var meta []string
	if len(m.Tags) > 0 {
		meta = append(meta, "tags: "+strings.Join(m.Tags, ", "))
	}
	if m.Source != "" {
		meta = append(meta, "source: "+m.Source)
	}
	meta = append(meta, "updated: "+m.UpdatedAt.Format("2006-01-02"))
	if m.Archived {
		meta = append(meta, "ARCHIVED")
	}
	fmt.Fprintf(w, "    %s\n", strings.Join(meta, " | "))
}

// parseID parses a memory id, accepting "#12" as well as "12".
func parseID(s string) (int64, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	var id int64
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid memory id %q", s)
	}
	return id, nil
}

// splitCSV splits a comma-separated flag value.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readInput returns the positional text, or stdin when the argument is "-".
func readInput(args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("missing text (pass text as an argument or - to read stdin)")
	}
	if args[0] == "-" {
		return readAllStdin()
	}
	return strings.Join(args, " "), nil
}

// readAllStdin reads all of standard input.
func readAllStdin() (string, error) {
	data, err := io.ReadAll(os.Stdin)
	return string(data), err
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return truncateText(line, 120)
		}
	}
	return ""
}

// truncateText shortens s to n runes.
func truncateText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// formatCounts renders a count map sorted by key.
func formatCounts(counts map[string]int64) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

// mask hides a secret while showing whether one is configured.
func mask(secret string) string {
	if secret == "" {
		return "(unset)"
	}
	return "(set)"
}
