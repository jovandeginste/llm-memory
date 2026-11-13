package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"llm-memory/internal/config"
	"llm-memory/internal/memory"
	"llm-memory/internal/store"
)

// newInitCmd creates the database and config file.
func newInitCmd(a *App) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create the database and config",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(a.cfgPath); err == nil && !force {
				fmt.Printf("config exists: %s\n", a.cfgPath)
			} else {
				if err := config.Save(a.cfgPath, config.Defaults()); err != nil {
					return err
				}
				fmt.Printf("wrote config: %s\n", a.cfgPath)
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			st, err := svc.Stats()
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(st)
			}
			fmt.Printf("database: %s\n", st.DB)
			fmt.Printf("embedder: %s (%d dims)\n", st.Embedder, st.Dims)
			fmt.Printf("memories: %d live, %d archived, %d links\n", st.Memories, st.Archived, st.Links)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}

// newAddCmd stores one memory, or a stream of JSONL memories from stdin.
func newAddCmd(a *App) *cobra.Command {
	var category, tags, source, session string
	var importance, threshold float64
	var noLink, jsonl bool
	var topK int
	cmd := &cobra.Command{
		Use:   "add <text|->",
		Short: "Store a memory and auto-link it",
		Long: `Store one memory, or read a stream from stdin with "-".

With "-", each non-empty input line may be a JSON object with content,
category, importance, tags, source, and session fields. Command-line flags
supply defaults for fields a line omits. Plain text on stdin is stored as a
single memory.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonl && (len(args) != 1 || args[0] != "-") {
				return errors.New("--jsonl requires reading from stdin (-)")
			}
			link := !noLink
			in := memory.AddInput{
				Category: category, Importance: importance,
				Tags: splitCSV(tags), Source: source, Session: session,
				Link: &link, Threshold: threshold, TopK: topK,
			}
			if len(args) == 1 && args[0] == "-" {
				return runAddStdin(cmd, a, in, jsonl)
			}
			text, err := readInput(args)
			if err != nil {
				return err
			}
			in.Content = text
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			res, err := svc.Add(cmd.Context(), in)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(addDTO(res))
			}
			printAddResult(res)
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "fact", "category: "+strings.Join(memory.Categories, ", "))
	cmd.Flags().Float64Var(&importance, "importance", 0.5, "importance 0..1")
	cmd.Flags().StringVar(&tags, "tags", "", "comma-separated tags")
	cmd.Flags().StringVar(&source, "source", "", "source reference")
	cmd.Flags().StringVar(&session, "session", "", "session id")
	cmd.Flags().BoolVar(&noLink, "no-link", false, "do not create automatic links")
	cmd.Flags().Float64Var(&threshold, "threshold", 0, "similarity threshold for automatic links (default from config)")
	cmd.Flags().IntVar(&topK, "topk", 0, "maximum automatic links (default from config)")
	cmd.Flags().BoolVar(&jsonl, "jsonl", false, "read one JSON memory object per line from stdin")
	return cmd
}

// printAddResult renders one stored memory and its new links.
func printAddResult(res *memory.AddResult) {
	memoryLine(os.Stdout, res.Memory, "")
	if len(res.Links) == 0 {
		fmt.Println("    no related memories above the link threshold")
		return
	}
	fmt.Println("    linked to:")
	for _, l := range res.Links {
		fmt.Printf("      #%d (similarity %.2f)\n", l.Dst, l.Similarity)
	}
}

// newIngestCmd turns raw text into several memories.
func newIngestCmd(a *App) *cobra.Command {
	var category, source, session, tags string
	var importance float64
	var useExtract bool
	cmd := &cobra.Command{
		Use:   "ingest <file|->",
		Short: "Split raw text into atomic memories",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var text string
			if len(args) == 0 || args[0] == "-" {
				data, err := readAllStdin()
				if err != nil {
					return err
				}
				text = data
			} else {
				data, err := os.ReadFile(args[0])
				if err != nil {
					return err
				}
				text = string(data)
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			results, err := svc.Ingest(cmd.Context(), text, memory.IngestOptions{
				Category: category, Source: source, Session: session,
				Importance: importance, Tags: splitCSV(tags), Extract: useExtract,
			})
			if err != nil {
				return err
			}
			if a.json {
				out := make([]memoryJSON, 0, len(results))
				for _, r := range results {
					out = append(out, memoryDTO(r.Memory))
				}
				return a.printJSON(out)
			}
			fmt.Printf("stored %d memories\n", len(results))
			for _, r := range results {
				fmt.Printf("  #%d [%s] %s\n", r.Memory.ID, r.Memory.Category, firstLine(r.Memory.Content))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "fact", "default category")
	cmd.Flags().StringVar(&source, "source", "", "source reference")
	cmd.Flags().StringVar(&session, "session", "", "session id")
	cmd.Flags().Float64Var(&importance, "importance", 0.5, "default importance")
	cmd.Flags().StringVar(&tags, "tags", "", "comma-separated tags")
	cmd.Flags().BoolVar(&useExtract, "extract", true, "use the configured LLM extractor when available")
	return cmd
}

// newSearchCmd runs a hybrid search.
func newSearchCmd(a *App) *cobra.Command {
	var category string
	var k int
	var minSim float64
	var depth int
	var noTouch bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Hybrid vector and keyword search",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.Join(args, " ")
			if query == "" {
				return errors.New("missing search query")
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			results, err := svc.Search(cmd.Context(), memory.SearchInput{
				Query: query, K: k, Category: category,
				MinSimilarity: minSim, GraphDepth: depth, Touch: !noTouch,
			})
			if err != nil {
				return err
			}
			if a.json {
				type resultJSON struct {
					memoryJSON
					Score      float64 `json:"score"`
					Similarity float64 `json:"similarity"`
					Via        string  `json:"via"`
				}
				out := make([]resultJSON, 0, len(results))
				for _, r := range results {
					out = append(out, resultJSON{memoryDTO(r.Memory), r.Score, r.Similarity, r.Via})
				}
				return a.printJSON(out)
			}
			if len(results) == 0 {
				fmt.Println("no matches")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTR\tSCORE\tSIM\tVIA\tCATEGORY\tCONTENT")
			for _, r := range results {
				fmt.Fprintf(tw, "#%d\t%.2f\t%.3f\t%.2f\t%s\t%s\t%s\n",
					r.Memory.ID, r.Memory.Strength, r.Score, r.Similarity, r.Via,
					r.Memory.Category, truncateText(r.Memory.Content, 90))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().IntVar(&k, "k", 10, "number of results")
	cmd.Flags().StringVar(&category, "category", "", "restrict to one category")
	cmd.Flags().Float64Var(&minSim, "min-similarity", 0, "drop vector hits below this cosine similarity")
	cmd.Flags().IntVar(&depth, "graph-depth", 0, "expand results over the link graph N hops")
	cmd.Flags().BoolVar(&noTouch, "no-touch", false, "do not count results as accessed")
	return cmd
}

// newGetCmd shows one memory with its links.
func newGetCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show a memory with its links",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			d, err := svc.Get(id)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(detailDTO(d))
			}
			memoryLine(os.Stdout, d.Memory, "")
			fmt.Printf("    importance: %.2f | access count: %d | created: %s\n",
				d.Memory.Importance, d.Memory.Access, d.Memory.CreatedAt.Format("2006-01-02"))
			if len(d.Links) == 0 {
				fmt.Println("    no links")
				return nil
			}
			fmt.Println("    links:")
			for _, l := range d.Links {
				fmt.Printf("      #%d %.2f %s\n        %s\n", l.Link.Dst, l.Link.Similarity,
					l.Other.Category, truncateText(l.Other.Content, 160))
			}
			return nil
		},
	}
}

// newListCmd lists recent memories.
func newListCmd(a *App) *cobra.Command {
	var category string
	var limit, offset int
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent memories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			mems, err := svc.List(store.Filter{
				Category: category, IncludeArchived: all,
				Limit: limit, Offset: offset,
			})
			if err != nil {
				return err
			}
			if a.json {
				out := make([]memoryJSON, 0, len(mems))
				for _, m := range mems {
					out = append(out, memoryDTO(m))
				}
				return a.printJSON(out)
			}
			if len(mems) == 0 {
				fmt.Println("no memories")
				return nil
			}
			for _, m := range mems {
				memoryLine(os.Stdout, m, "")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&category, "category", "", "restrict to one category")
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum memories")
	cmd.Flags().IntVar(&offset, "offset", 0, "skip the first N memories")
	cmd.Flags().BoolVar(&all, "include-archived", false, "include archived memories")
	return cmd
}

// newUpdateCmd changes a memory.
func newUpdateCmd(a *App) *cobra.Command {
	var content, category, tags, source string
	var importance float64
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Change a memory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			changed := cmd.Flags().Changed("content") || cmd.Flags().Changed("category") ||
				cmd.Flags().Changed("importance") || cmd.Flags().Changed("tags") ||
				cmd.Flags().Changed("source")
			if !changed {
				return errors.New("nothing to update; pass at least one flag")
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			m, err := svc.Update(cmd.Context(), id, func(m *store.Memory) error {
				if cmd.Flags().Changed("content") {
					m.Content = content
				}
				if cmd.Flags().Changed("category") {
					m.Category = category
				}
				if cmd.Flags().Changed("importance") {
					m.Importance = importance
				}
				if cmd.Flags().Changed("tags") {
					m.Tags = splitCSV(tags)
				}
				if cmd.Flags().Changed("source") {
					m.Source = source
				}
				return nil
			})
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(memoryDTO(m))
			}
			memoryLine(os.Stdout, m, "")
			return nil
		},
	}
	cmd.Flags().StringVar(&content, "content", "", "new content")
	cmd.Flags().StringVar(&category, "category", "", "new category")
	cmd.Flags().Float64Var(&importance, "importance", -1, "new importance 0..1")
	cmd.Flags().StringVar(&tags, "tags", "", "new comma-separated tags")
	cmd.Flags().StringVar(&source, "source", "", "new source")
	return cmd
}

// newDeleteCmd archives or deletes a memory.
func newDeleteCmd(a *App) *cobra.Command {
	var hard bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Archive a memory, or delete it permanently with --hard",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			if err := svc.Delete(cmd.Context(), id, hard); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]any{"id": id, "deleted": hard})
			}
			if hard {
				fmt.Printf("deleted #%d\n", id)
			} else {
				fmt.Printf("archived #%d (use --hard to delete permanently)\n", id)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&hard, "hard", false, "delete permanently instead of archiving")
	return cmd
}

// newRestoreCmd unarchives a memory.
func newRestoreCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id>",
		Short: "Bring an archived memory back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			m, err := svc.Restore(cmd.Context(), id)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(memoryDTO(m))
			}
			fmt.Printf("restored #%d\n", id)
			memoryLine(os.Stdout, m, "")
			return nil
		},
	}
}

// newReinforceCmd boosts a memory.
func newReinforceCmd(a *App) *cobra.Command {
	var amount float64
	cmd := &cobra.Command{
		Use:   "reinforce <id>",
		Short: "Boost a memory that proved useful",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			m, err := svc.Reinforce(id, amount)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(memoryDTO(m))
			}
			memoryLine(os.Stdout, m, "")
			return nil
		},
	}
	cmd.Flags().Float64Var(&amount, "amount", 0.1, "importance increase")
	return cmd
}

// newLinkCmd links two memories.
func newLinkCmd(a *App) *cobra.Command {
	var similarity float64
	cmd := &cobra.Command{
		Use:   "link <src> <dst>",
		Short: "Create a bidirectional link",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := parseID(args[0])
			if err != nil {
				return err
			}
			dst, err := parseID(args[1])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			if err := svc.Link(src, dst, similarity); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]any{"src": src, "dst": dst, "similarity": similarity})
			}
			fmt.Printf("linked #%d <-> #%d (%.2f)\n", src, dst, similarity)
			return nil
		},
	}
	cmd.Flags().Float64Var(&similarity, "similarity", 1, "link strength 0..1")
	return cmd
}

// newUnlinkCmd removes a link.
func newUnlinkCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "unlink <src> <dst>",
		Short: "Remove a link",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := parseID(args[0])
			if err != nil {
				return err
			}
			dst, err := parseID(args[1])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			if err := svc.Unlink(src, dst); err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]any{"src": src, "dst": dst, "unlinked": true})
			}
			fmt.Printf("unlinked #%d <-> #%d\n", src, dst)
			return nil
		},
	}
}

// newLinksCmd lists a memory's links.
func newLinksCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "links <id>",
		Short: "List a memory's links",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			d, err := svc.Get(id)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(detailDTO(d))
			}
			if len(d.Links) == 0 {
				fmt.Printf("#%d has no links\n", id)
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSIM\tCATEGORY\tCONTENT")
			for _, l := range d.Links {
				fmt.Fprintf(tw, "#%d\t%.2f\t%s\t%s\n", l.Link.Dst, l.Link.Similarity,
					l.Other.Category, truncateText(l.Other.Content, 80))
			}
			return tw.Flush()
		},
	}
}
