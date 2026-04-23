// Package cli provides the shared Cobra root command for `wfctl migrate *`
// and the standalone `workflow-migrate` binary. Both entry points call the
// same command tree so the behaviour is identical regardless of invocation.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/GoCodeAlone/workflow/interfaces"

	atlasdriver "github.com/GoCodeAlone/workflow-plugin-migrations/internal/atlas"
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/goose"
)

// cliProvider implements sdk.CLIProvider by dispatching to the Cobra root.
type cliProvider struct{}

// NewCLIProvider returns a new CLIProvider.
func NewCLIProvider() *cliProvider { return &cliProvider{} }

// RunCLI implements sdk.CLIProvider.
func (c *cliProvider) RunCLI(args []string) int {
	root := NewRoot()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// NewRoot builds the Cobra root command for the migrate CLI.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "migrate",
		Short: "Database migration commands",
		Long:  "Run, inspect, and test database migrations via golang-migrate, goose, or atlas.",
	}
	root.AddCommand(
		newUpCmd(),
		newDownCmd(),
		newStatusCmd(),
		newGotoCmd(),
		newLintCmd(),
	)
	return root
}

// sharedFlags adds the common driver/DSN/source-dir flags to a command.
func sharedFlags(cmd *cobra.Command) {
	cmd.Flags().String("driver", "golang-migrate", "Migration driver (golang-migrate|goose|atlas)")
	cmd.Flags().String("source-dir", "", "Directory containing migration files (required)")
	cmd.Flags().String("dsn", "", "Database connection string (overrides DATABASE_URL env var)")
	_ = cmd.MarkFlagRequired("source-dir")
}

// buildDriverAndRequest resolves the driver and constructs a MigrationRequest from flags.
func buildDriverAndRequest(cmd *cobra.Command) (interfaces.MigrationDriver, interfaces.MigrationRequest, error) {
	driverName, _ := cmd.Flags().GetString("driver")
	sourceDir, _ := cmd.Flags().GetString("source-dir")
	dsn, _ := cmd.Flags().GetString("dsn")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return nil, interfaces.MigrationRequest{}, fmt.Errorf("no DSN: set --dsn or DATABASE_URL env var")
	}

	var d interfaces.MigrationDriver
	switch driverName {
	case "golang-migrate", "":
		d = golangmigrate.New()
	case "goose":
		d = goose.New()
	case "atlas":
		d = atlasdriver.New()
	default:
		return nil, interfaces.MigrationRequest{}, fmt.Errorf("unknown driver %q (supported: golang-migrate, goose, atlas)", driverName)
	}

	req := interfaces.MigrationRequest{
		DSN:    dsn,
		Source: interfaces.MigrationSource{Dir: sourceDir},
	}
	return d, req, nil
}

func newUpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			result, err := d.Up(context.Background(), req)
			if err != nil {
				return fmt.Errorf("migrate up: %w", err)
			}
			if len(result.Applied) == 0 {
				fmt.Println("No pending migrations.")
				return nil
			}
			fmt.Printf("Applied %d migration(s): %v\n", len(result.Applied), result.Applied)
			return nil
		},
	}
	sharedFlags(cmd)
	return cmd
}

func newDownCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Roll back N migrations (default: 1)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			steps, _ := cmd.Flags().GetInt("steps")
			req.Options.Steps = steps
			result, err := d.Down(context.Background(), req)
			if err != nil {
				return fmt.Errorf("migrate down: %w", err)
			}
			fmt.Printf("Rolled back %d migration(s): %v\n", len(result.Applied), result.Applied)
			return nil
		},
	}
	sharedFlags(cmd)
	cmd.Flags().Int("steps", 1, "Number of migrations to roll back")
	return cmd
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current migration status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			st, err := d.Status(context.Background(), req)
			if err != nil {
				return fmt.Errorf("migrate status: %w", err)
			}
			if st.Current == "" {
				fmt.Println("No migrations applied.")
			} else {
				fmt.Printf("Current: %s\n", st.Current)
			}
			if len(st.Pending) > 0 {
				fmt.Printf("Pending: %v\n", st.Pending)
			} else {
				fmt.Println("No pending migrations.")
			}
			if st.Dirty {
				fmt.Println("WARNING: database is in dirty state!")
			}
			return nil
		},
	}
	sharedFlags(cmd)
	return cmd
}

func newGotoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "goto <version>",
		Short: "Migrate to a specific version (up or down)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			target := args[0]
			result, err := d.Goto(context.Background(), req, target)
			if err != nil {
				return fmt.Errorf("migrate goto %s: %w", target, err)
			}
			fmt.Printf("Migrated to %s (%d steps): %v\n", target, len(result.Applied), result.Applied)
			return nil
		},
	}
	sharedFlags(cmd)
	return cmd
}
