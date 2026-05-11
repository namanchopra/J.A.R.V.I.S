package cli

import (
	"fmt"
	"strings"

	"github.com/namanchopra/jarvis/internal/model"
	"github.com/namanchopra/jarvis/internal/store"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// add
// ---------------------------------------------------------------------------

func newAddCmd(s *store.Store) *cobra.Command {
	var (
		name        string
		repo        string
		agent       string
		description string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new task",
		Long:  "Create a new AI coding task and persist it to the local database.",
		Example: `  awm add --name "Refactor auth" --repo /projects/backend --agent claude-code
  awm add --name "Fix tests" --repo ./api --agent codex --description "Unit tests are flaky"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate agent type.
			agentType := model.AgentType(agent)
			if !model.ValidAgentType(agentType) {
				valid := make([]string, 0, len(model.AllAgentTypes()))
				for _, a := range model.AllAgentTypes() {
					valid = append(valid, string(a))
				}
				return fmt.Errorf("invalid agent type %q (valid: %s)", agent, strings.Join(valid, ", "))
			}

			task := model.NewTask(name, description, repo, agentType)
			created, err := s.CreateTask(task)
			if err != nil {
				return fmt.Errorf("creating task: %w", err)
			}

			if outputFormat == "json" {
				printJSON(created)
				return nil
			}

			fmt.Printf("Created task %s\n", created.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "task name (required)")
	cmd.Flags().StringVar(&repo, "repo", "", "repository path (required)")
	cmd.Flags().StringVar(&agent, "agent", "", "AI agent type (required)")
	cmd.Flags().StringVar(&description, "description", "", "optional task description")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("repo")
	_ = cmd.MarkFlagRequired("agent")

	// Register completion for --agent flag.
	_ = cmd.RegisterFlagCompletionFunc("agent", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		types := model.AllAgentTypes()
		out := make([]string, 0, len(types))
		for _, t := range types {
			out = append(out, string(t))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// ---------------------------------------------------------------------------
// list
// ---------------------------------------------------------------------------

func newListCmd(s *store.Store) *cobra.Command {
	var (
		status string
		repo   string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tasks",
		Long:  "List all tasks, optionally filtered by status and/or repository path.",
		Example: `  awm list
  awm list --status running
  awm list --repo /projects/backend
  awm list --status done --output json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate status filter if provided.
			if status != "" && !model.ValidStatus(model.Status(status)) {
				valid := make([]string, 0, len(model.AllStatuses()))
				for _, st := range model.AllStatuses() {
					valid = append(valid, string(st))
				}
				return fmt.Errorf("invalid status %q (valid: %s)", status, strings.Join(valid, ", "))
			}

			tasks, err := s.ListTasks(status, repo)
			if err != nil {
				return fmt.Errorf("listing tasks: %w", err)
			}

			if outputFormat == "json" {
				printJSON(tasks)
				return nil
			}

			if len(tasks) == 0 {
				fmt.Println("No tasks found.")
				return nil
			}

			printTaskTable(tasks)
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "filter by status (pending, running, done, failed, needs-input)")
	cmd.Flags().StringVar(&repo, "repo", "", "filter by repository path (substring match)")

	// Register completion for --status flag.
	_ = cmd.RegisterFlagCompletionFunc("status", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		statuses := model.AllStatuses()
		out := make([]string, 0, len(statuses))
		for _, st := range statuses {
			out = append(out, string(st))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func newUpdateCmd(s *store.Store) *cobra.Command {
	var (
		status      string
		outputPath  string
		name        string
		description string
	)

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a task",
		Long:  "Update one or more fields of an existing task by its ID (or unique prefix).",
		Example: `  awm update abc12345 --status running
  awm update abc12345 --name "New name" --description "Updated scope"
  awm update abc12345 --output-path /tmp/agent.log`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Resolve a short prefix to a full ID.
			fullID, err := resolveTaskID(s, id)
			if err != nil {
				return err
			}

			updates := make(map[string]interface{})

			if cmd.Flags().Changed("status") {
				if !model.ValidStatus(model.Status(status)) {
					valid := make([]string, 0, len(model.AllStatuses()))
					for _, st := range model.AllStatuses() {
						valid = append(valid, string(st))
					}
					return fmt.Errorf("invalid status %q (valid: %s)", status, strings.Join(valid, ", "))
				}
				updates["status"] = status
			}
			if cmd.Flags().Changed("output-path") {
				updates["output_path"] = outputPath
			}
			if cmd.Flags().Changed("name") {
				updates["name"] = name
			}
			if cmd.Flags().Changed("description") {
				updates["description"] = description
			}

			if len(updates) == 0 {
				return fmt.Errorf("no fields to update — specify at least one flag")
			}

			updated, err := s.UpdateTask(fullID, updates)
			if err != nil {
				return fmt.Errorf("updating task: %w", err)
			}

			if outputFormat == "json" {
				printJSON(updated)
				return nil
			}

			printTaskTable([]model.Task{updated})
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "new status")
	cmd.Flags().StringVar(&outputPath, "output-path", "", "path to agent output/log file")
	cmd.Flags().StringVar(&name, "name", "", "new task name")
	cmd.Flags().StringVar(&description, "description", "", "new task description")

	// Register completion for --status flag.
	_ = cmd.RegisterFlagCompletionFunc("status", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		statuses := model.AllStatuses()
		out := make([]string, 0, len(statuses))
		for _, st := range statuses {
			out = append(out, string(st))
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// ---------------------------------------------------------------------------
// remove
// ---------------------------------------------------------------------------

func newRemoveCmd(s *store.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <id>",
		Short: "Remove a task",
		Long:  "Permanently delete a task by its ID (or unique prefix).",
		Example: `  awm remove abc12345
  awm remove abc12345-6789-...`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			fullID, err := resolveTaskID(s, id)
			if err != nil {
				return err
			}

			if err := s.DeleteTask(fullID); err != nil {
				return fmt.Errorf("removing task: %w", err)
			}

			if outputFormat == "json" {
				printJSON(map[string]string{
					"deleted": fullID,
				})
				return nil
			}

			fmt.Printf("Removed task %s\n", fullID)
			return nil
		},
	}

	return cmd
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// resolveTaskID resolves a potentially short ID prefix to the full task UUID.
// If the user passes a full UUID it is returned as-is after verifying existence.
// If a short prefix is given, all tasks are listed and matched by prefix; the
// match must be unique.
func resolveTaskID(s *store.Store, prefix string) (string, error) {
	// Try exact match first (fast path for full UUIDs).
	if _, err := s.GetTask(prefix); err == nil {
		return prefix, nil
	}

	// Fall back to prefix matching.
	tasks, err := s.ListTasks("", "")
	if err != nil {
		return "", fmt.Errorf("listing tasks for prefix resolution: %w", err)
	}

	var matches []string
	for _, t := range tasks {
		if strings.HasPrefix(t.ID, prefix) {
			matches = append(matches, t.ID)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no task found matching %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("prefix %q is ambiguous — matches %d tasks (use more characters)", prefix, len(matches))
	}
}
