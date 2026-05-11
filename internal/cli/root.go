package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/store"

	"github.com/spf13/cobra"
)

var (
	rootCmd      *cobra.Command
	outputFormat string
)

// NewRootCmd creates and returns the root cobra command for the Jarvis CLI.
// The provided store is threaded through to every subcommand.
func NewRootCmd(s *store.Store) *cobra.Command {
	rootCmd = &cobra.Command{
		Use:   "awm",
		Short: "Jarvis — track your AI coding tasks",
		Long: `Jarvis lets you create, list, update, and remove
tasks that are delegated to AI coding agents such as Claude Code,
Kiro, Gemini, Codex, or Aider.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "output format: table or json")

	rootCmd.AddCommand(
		newAddCmd(s),
		newListCmd(s),
		newUpdateCmd(s),
		newRemoveCmd(s),
	)

	return rootCmd
}

// printJSON marshals v as indented JSON and writes it to stdout.
func printJSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error marshalling JSON: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

// printTaskTable prints tasks as a formatted table to stdout.
// Columns: ID (first 8 chars), STATUS, NAME, REPO, AGENT, UPDATED.
func printTaskTable(tasks []model.Task) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tNAME\tREPO\tAGENT\tUPDATED")
	fmt.Fprintln(w, strings.Repeat("-", 8)+"\t"+
		strings.Repeat("-", 11)+"\t"+
		strings.Repeat("-", 20)+"\t"+
		strings.Repeat("-", 20)+"\t"+
		strings.Repeat("-", 12)+"\t"+
		strings.Repeat("-", 19))

	for _, t := range tasks {
		id := t.ID
		if len(id) > 8 {
			id = id[:8]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			id,
			t.Status,
			t.Name,
			t.RepoPath,
			t.AgentType,
			t.UpdatedAt.Format("2006-01-02 15:04:05"),
		)
	}
	w.Flush()
}
