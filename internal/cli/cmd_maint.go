package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"llm-memory/internal/embed"
	"llm-memory/internal/memory"
)

// newContextCmd rebuilds the session context summary.
func newContextCmd(a *App) *cobra.Command {
	var days, limit int
	var write string
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Rebuild a markdown context summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			markdown, err := svc.Context(cmd.Context(), memory.ContextOptions{
				Days: days, Limit: limit,
			})
			if err != nil {
				return err
			}
			if write != "" {
				if err := os.WriteFile(write, []byte(markdown), 0o644); err != nil {
					return err
				}
				if a.json {
					return a.printJSON(map[string]any{"file": write, "bytes": len(markdown)})
				}
				fmt.Printf("wrote %s (%d bytes)\n", write, len(markdown))
				return nil
			}
			if a.json {
				return a.printJSON(map[string]any{"markdown": markdown})
			}
			fmt.Print(markdown)
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "include memories updated in the last N days")
	cmd.Flags().IntVar(&limit, "limit", 40, "maximum memories in the summary")
	cmd.Flags().StringVar(&write, "write", "", "write the summary to this file instead of stdout")
	return cmd
}

// newDecayCmd archives weak memories.
func newDecayCmd(a *App) *cobra.Command {
	var dryRun, purge bool
	var archiveAt, purgeAt float64
	cmd := &cobra.Command{
		Use:   "decay",
		Short: "Archive weak memories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			report, err := svc.Decay(cmd.Context(), memory.DecayOptions{
				DryRun: dryRun, Purge: purge, ArchiveAt: archiveAt, PurgeAt: purgeAt,
			})
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(report)
			}
			mode := ""
			if dryRun {
				mode = " (dry run)"
			}
			fmt.Printf("scanned %d memories%s: %d archived, %d purged\n", report.Scanned, mode, report.Archived, report.Purged)
			if len(report.Actions) == 0 {
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ACTION\tID\tSTRENGTH\tCONTENT")
			for _, act := range report.Actions {
				fmt.Fprintf(tw, "%s\t#%d\t%.2f\t%s\n", act.Action, act.ID, act.Strength, truncateText(act.Content, 70))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report without changing anything")
	cmd.Flags().BoolVar(&purge, "purge", false, "hard-delete memories below the purge threshold")
	cmd.Flags().Float64Var(&archiveAt, "archive-below", 0.15, "archive memories with strength below this")
	cmd.Flags().Float64Var(&purgeAt, "purge-below", 0.05, "purge memories with strength below this (needs --purge)")
	return cmd
}

// newStatsCmd prints the health summary.
func newStatsCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show database health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			fmt.Printf("database:      %s\n", st.DB)
			fmt.Printf("embedder:      %s (%d dims)\n", st.Embedder, st.Dims)
			fmt.Printf("memories:      %d live, %d archived\n", st.Memories, st.Archived)
			fmt.Printf("links:         %d (%.2f per memory)\n", st.Links, st.AvgLinks)
			if st.LastUpdated != "" {
				fmt.Printf("last update:   %s\n", st.LastUpdated)
			}
			if len(st.ByCategory) > 0 {
				fmt.Printf("categories:    %s\n", formatCounts(st.ByCategory))
			}
			return nil
		},
	}
}

// newReindexCmd re-embeds all memories.
func newReindexCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "reindex",
		Short: "Re-embed every memory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			start := time.Now()
			n, err := svc.Reindex(cmd.Context())
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(map[string]any{
					"memories": n, "embedder": svc.Embedder().Name(),
					"dims": svc.Dims(), "seconds": time.Since(start).Seconds(),
				})
			}
			fmt.Printf("reindexed %d memories with %s (%d dims) in %s\n",
				n, svc.Embedder().Name(), svc.Dims(), time.Since(start).Round(time.Millisecond))
			return nil
		},
	}
}

// newConfigCmd prints the resolved configuration.
func newConfigCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "Print the resolved configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := a.cfg
			if a.json {
				cfg.EmbedKey = mask(cfg.EmbedKey)
				cfg.ExtractKey = mask(cfg.ExtractKey)
				return a.printJSON(cfg)
			}
			fmt.Printf("config:          %s\n", a.cfgPath)
			fmt.Printf("db:              %s\n", cfg.DB)
			fmt.Printf("embedder:        %s\n", cfg.Embedder)
			fmt.Printf("ollama_url:      %s\n", cfg.OllamaURL)
			fmt.Printf("embed_model:     %s\n", cfg.EmbedModel)
			fmt.Printf("embed_url:       %s\n", cfg.EmbedURL)
			fmt.Printf("embed_api_key:   %s\n", mask(cfg.EmbedKey))
			fmt.Printf("link_threshold:  %.2f\n", cfg.LinkThreshold)
			fmt.Printf("link_topk:       %d\n", cfg.LinkTopK)
			fmt.Printf("extract_url:     %s\n", cfg.ExtractURL)
			fmt.Printf("extract_model:   %s\n", cfg.ExtractModel)
			return nil
		},
	}
}

// newEmbedCmd prints an embedding for debugging.
func newEmbedCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "embed <text|->",
		Short: "Print an embedding (debug)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			text, err := readInput(args)
			if err != nil {
				return err
			}
			emb, err := embed.New(cmd.Context(), a.cfg)
			if err != nil {
				return err
			}
			vecs, err := emb.Embed(cmd.Context(), []string{text})
			if err != nil {
				return err
			}
			if len(vecs) == 0 {
				return errors.New("embedder returned no vectors")
			}
			vec := vecs[0]
			if a.json {
				return a.printJSON(map[string]any{"embedder": emb.Name(), "dims": len(vec), "vector": vec})
			}
			n := len(vec)
			if n > 8 {
				n = 8
			}
			parts := make([]string, n)
			for i := 0; i < n; i++ {
				parts[i] = fmt.Sprintf("%.4f", vec[i])
			}
			fmt.Printf("embedder: %s\ndims: %d\nfirst values: [%s ...]\n", emb.Name(), len(vec), strings.Join(parts, ", "))
			return nil
		},
	}
}

// newVersionCmd prints the version.
func newVersionCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.json {
				return a.printJSON(map[string]string{"version": Version})
			}
			fmt.Println("llmmem " + Version)
			return nil
		},
	}
}
