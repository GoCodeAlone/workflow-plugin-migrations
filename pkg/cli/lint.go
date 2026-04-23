package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/lint"
)

func newLintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint <dir>",
		Short: "Static analysis for migration files",
		Long: `Lint checks a migration directory for common issues:
  L001 - version ordering gap
  L002 - duplicate version
  L003 - naming convention violation
  L004 - dangerous SQL operation without safety guard
  L005 - missing .down.sql counterpart (paired format)
  L006 - empty migration file`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			jsonOut, _ := cmd.Flags().GetBool("json")

			result, err := lint.Lint(dir)
			if err != nil {
				return fmt.Errorf("lint: %w", err)
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			if result.Clean {
				fmt.Println("✓ No issues found.")
				return nil
			}

			for _, issue := range result.Issues {
				loc := issue.File
				if loc == "" && issue.Version != "" {
					loc = "v" + issue.Version
				}
				fmt.Printf("[%s] %s (%s): %s\n", issue.Severity, issue.Code, loc, issue.Message)
			}

			errCount := 0
			for _, i := range result.Issues {
				if i.Severity == lint.SeverityError {
					errCount++
				}
			}
			if errCount > 0 {
				return fmt.Errorf("lint: %d error(s) found", errCount)
			}
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "Output results as JSON")
	return cmd
}
