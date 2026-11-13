package cli

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// newGraphCmd groups the read-only graph queries.
func newGraphCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Inspect the memory graph",
	}
	cmd.AddCommand(
		newGraphTraverseCmd(a),
		newGraphPathCmd(a),
		newGraphClustersCmd(a),
		newGraphOrphansCmd(a),
	)
	return cmd
}

// newGraphTraverseCmd walks the graph from a memory.
func newGraphTraverseCmd(a *App) *cobra.Command {
	var hops int
	cmd := &cobra.Command{
		Use:   "traverse <id>",
		Short: "Walk the link graph from a memory",
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
			nodes, err := svc.Traverse(id, hops)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(nodes)
			}
			fmt.Printf("traversal from #%d, %d hops, %d memories\n", id, hops, len(nodes))
			for _, n := range nodes {
				m, err := svc.Get(n.ID)
				if err != nil {
					continue
				}
				via := ""
				if n.Hop > 0 {
					via = fmt.Sprintf(" via #%d", n.Via)
				}
				fmt.Printf("  hop %d  #%-4d %-10s %s%s\n", n.Hop, n.ID, m.Memory.Category,
					truncateText(m.Memory.Content, 90), via)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&hops, "hops", 2, "number of hops")
	return cmd
}

// newGraphPathCmd prints the shortest path between two memories.
func newGraphPathCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "path <from> <to>",
		Short: "Shortest path between two memories",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, err := parseID(args[0])
			if err != nil {
				return err
			}
			to, err := parseID(args[1])
			if err != nil {
				return err
			}
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			path, err := svc.Path(from, to)
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(path)
			}
			fmt.Printf("path length %d\n", len(path)-1)
			for i, id := range path {
				m, err := svc.Get(id)
				if err != nil {
					continue
				}
				arrow := "  "
				if i > 0 {
					arrow = "->"
				}
				fmt.Printf("%s #%-4d %s\n", arrow, id, truncateText(m.Memory.Content, 100))
			}
			return nil
		},
	}
}

// newGraphClustersCmd prints the connected components.
func newGraphClustersCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "clusters",
		Short: "List connected components",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			clusters, err := svc.Clusters()
			if err != nil {
				return err
			}
			if a.json {
				return a.printJSON(clusters)
			}
			if len(clusters) == 0 {
				fmt.Println("no memories")
				return nil
			}
			for i, c := range clusters {
				fmt.Printf("cluster %d: %d memories\n", i+1, c.Size)
				fmt.Printf("  categories: %s\n", joinCounts(c.Categories))
				if len(c.TopTags) > 0 {
					fmt.Printf("  top tags: %v\n", c.TopTags)
				}
				ids := make([]string, 0, 10)
				for _, id := range c.Members {
					if len(ids) == 10 {
						ids = append(ids, "...")
						break
					}
					ids = append(ids, fmt.Sprintf("#%d", id))
				}
				fmt.Printf("  members: %v\n", ids)
			}
			return nil
		},
	}
}

// newGraphOrphansCmd prints memories without links.
func newGraphOrphansCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "orphans",
		Short: "List memories with no links",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := a.service(cmd.Context())
			if err != nil {
				return err
			}
			defer svc.Close()
			mems, err := svc.Orphans()
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
				fmt.Println("no orphans")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTR\tCATEGORY\tCONTENT")
			for _, m := range mems {
				fmt.Fprintf(tw, "#%d\t%.2f\t%s\t%s\n", m.ID, m.Strength, m.Category,
					truncateText(m.Content, 80))
			}
			return tw.Flush()
		},
	}
}

// joinCounts renders a count map as "key=n, key=n" sorted by count.
func joinCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if counts[keys[a]] == counts[keys[b]] {
			return keys[a] < keys[b]
		}
		return counts[keys[a]] > counts[keys[b]]
	})
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s=%d", k, counts[k])
	}
	return out
}
