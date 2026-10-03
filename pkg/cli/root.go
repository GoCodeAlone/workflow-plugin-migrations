// Package cli provides the shared Cobra root command for `wfctl db-migrate *`
// and the standalone `workflow-migrate` binary. Both entry points call the
// same command tree so the behaviour is identical regardless of invocation.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GoCodeAlone/workflow/interfaces"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/driver"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/runner"
)

// cliProvider implements sdk.CLIProvider by dispatching to the Cobra root.
type cliProvider struct{}

// NewCLIProvider returns a new CLIProvider.
func NewCLIProvider() *cliProvider { return &cliProvider{} }

// RunCLI implements sdk.CLIProvider.
func (c *cliProvider) RunCLI(args []string) int {
	root := NewRoot()
	// The SDK includes the dynamic command name; Cobra expects only its args.
	if len(args) > 0 && args[0] == root.Name() {
		args = args[1:]
	}
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// NewRoot builds the Cobra root command for the db-migrate CLI.
func NewRoot() *cobra.Command {
	return newRoot(driver.NewDefaultRegistry())
}

func newRoot(registry *driver.Registry) *cobra.Command {
	root := &cobra.Command{
		Use:          "db-migrate",
		SilenceUsage: true,
		Short:        "Database migration commands",
		Long:         "Run, inspect, and test database migrations via golang-migrate, goose, or atlas.",
	}
	root.AddCommand(
		newUpCmd(registry),
		newDownCmd(registry),
		newStatusCmd(registry),
		newGotoCmd(registry),
		newForceCmd(),
		newRepairDirtyCmd(),
		newValidateUpgradeCmd(),
		newLintCmd(),
		newTestCmd(),
		newTenantEnsureCmd(),
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

func runnerFlags(cmd *cobra.Command) {
	sharedFlags(cmd)
	cmd.Flags().Duration("timeout", 0, "Operation timeout (0 uses the caller deadline)")
}

// buildDriverAndRequest resolves the driver and constructs a MigrationRequest from flags.
func buildDriverAndRequest(cmd *cobra.Command) (interfaces.MigrationDriver, interfaces.MigrationRequest, error) {
	req, err := requestFromFlags(cmd)
	if err != nil {
		return nil, req, err
	}
	driverName, _ := cmd.Flags().GetString("driver")
	if driverName == "" {
		driverName = "golang-migrate"
	}
	d, err := driver.NewDefaultRegistry().Get(driverName)
	return d, req, err
}

func buildRunnerAndRequest(cmd *cobra.Command, registry *driver.Registry) (driver.Driver, driver.Request, error) {
	req, err := requestFromFlags(cmd)
	if err != nil {
		return nil, req, err
	}
	name, _ := cmd.Flags().GetString("driver")
	// Cobra owns presentation and error reporting; the runner still supplies
	// validation, cancellation, and redacted errors for every ordinary operation.
	r, err := runner.New(registry, name, io.Discard, io.Discard)
	return r, req, err
}

func requestFromFlags(cmd *cobra.Command) (driver.Request, error) {
	sourceDir, _ := cmd.Flags().GetString("source-dir")
	dsn, _ := cmd.Flags().GetString("dsn")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		return driver.Request{}, fmt.Errorf("no DSN: set --dsn or DATABASE_URL env var")
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")
	req := interfaces.MigrationRequest{
		DSN:     dsn,
		Source:  interfaces.MigrationSource{Dir: sourceDir},
		Options: interfaces.MigrationOptions{Timeout: timeout},
	}
	return req, nil
}

func newUpCmd(registry *driver.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		Long: `Apply all pending migrations against the configured database.

When --up-if-clean is set the command is idempotent: if no migrations are
pending (database already at latest version) the command exits 0 quietly
with an informative message. Without --up-if-clean the same condition also
exits 0 (since there is nothing to apply). The flag makes the deploy-script
intent explicit and, crucially, must be accepted by cobra so deploy CMDs
that pass it succeed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			upIfClean, _ := cmd.Flags().GetBool("up-if-clean")
			d, req, err := buildRunnerAndRequest(cmd, registry)
			if err != nil {
				return err
			}
			result, err := d.Up(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("migrate up: %w", err)
			}
			if len(result.Applied) == 0 {
				if upIfClean {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "up-if-clean: no pending migrations; database is clean.")
				} else {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "No pending migrations.")
				}
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Applied %d migration(s): %v\n", len(result.Applied), result.Applied)
			return err
		},
	}
	runnerFlags(cmd)
	cmd.Flags().Bool("up-if-clean", false, "Idempotent up: exit 0 when no migrations are pending. Required for deploy CMDs that may re-run against an already-current database.")
	return cmd
}

func newDownCmd(registry *driver.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Roll back N migrations (default: 1)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, req, err := buildRunnerAndRequest(cmd, registry)
			if err != nil {
				return err
			}
			steps, _ := cmd.Flags().GetInt("steps")
			req.Options.Steps = steps
			result, err := d.Down(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("migrate down: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Rolled back %d migration(s): %v\n", len(result.Applied), result.Applied)
			return err
		},
	}
	runnerFlags(cmd)
	cmd.Flags().Int("steps", 1, "Number of migrations to roll back")
	return cmd
}

func newStatusCmd(registry *driver.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current migration status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, req, err := buildRunnerAndRequest(cmd, registry)
			if err != nil {
				return err
			}
			st, err := d.Status(cmd.Context(), req)
			if err != nil {
				return fmt.Errorf("migrate status: %w", err)
			}
			return writeStatus(cmd.OutOrStdout(), st)
		},
	}
	runnerFlags(cmd)
	return cmd
}

func writeStatus(out io.Writer, st interfaces.MigrationStatus) error {
	var text string
	if st.Current == "" {
		text = "No migrations applied.\n"
	} else {
		text = fmt.Sprintf("Current: %s\n", st.Current)
	}
	if len(st.Pending) > 0 {
		text += fmt.Sprintf("Pending: %v\n", st.Pending)
	} else {
		text += "No pending migrations.\n"
	}
	text += fmt.Sprintf("Dirty: %t\n", st.Dirty)
	if st.Dirty {
		text += "WARNING: database is in dirty state!\n"
	}
	_, err := io.WriteString(out, text)
	return err
}

func newGotoCmd(registry *driver.Registry) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "goto <version>",
		Short: "Migrate to a specific version (up or down)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, req, err := buildRunnerAndRequest(cmd, registry)
			if err != nil {
				return err
			}
			target := args[0]
			result, err := d.Goto(cmd.Context(), req, target)
			if err != nil {
				return fmt.Errorf("migrate goto %s: %w", target, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Migrated to %s (%d steps): %v\n", target, len(result.Applied), result.Applied)
			return err
		},
	}
	runnerFlags(cmd)
	return cmd
}

type forceDriver interface {
	Force(ctx context.Context, req interfaces.MigrationRequest, target string, opts golangmigrate.ForceOptions) (interfaces.MigrationResult, error)
}

type repairDirtyDriver interface {
	RepairDirty(ctx context.Context, req interfaces.MigrationRequest, opts golangmigrate.RepairDirtyOptions) (interfaces.MigrationResult, error)
}

func newForceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "force <version>",
		Short:              "Force-set the recorded migration version",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, flagArgs, err := splitForceArgs(args)
			if err != nil {
				return err
			}
			if err := cmd.Flags().Parse(flagArgs); err != nil {
				return err
			}
			confirmation, _ := cmd.Flags().GetString("confirm-force")
			if confirmation != "FORCE_MIGRATION_METADATA" {
				return fmt.Errorf("force mutates migration metadata without applying SQL; pass --confirm-force FORCE_MIGRATION_METADATA to continue")
			}
			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			f, ok := d.(forceDriver)
			if !ok {
				return fmt.Errorf("driver %q does not support force", d.Name())
			}
			allowClean, _ := cmd.Flags().GetBool("allow-clean")
			result, err := f.Force(context.Background(), req, target, golangmigrate.ForceOptions{AllowClean: allowClean})
			if err != nil {
				return fmt.Errorf("migrate force %s: %w", target, err)
			}
			fmt.Printf("Recorded migration version set to %s; no migrations applied. Duration: %dms\n", target, result.DurationMs)
			return nil
		},
	}
	sharedFlags(cmd)
	cmd.Flags().String("confirm-force", "", "Typed confirmation required: FORCE_MIGRATION_METADATA")
	cmd.Flags().Bool("allow-clean", false, "Allow force-setting a database that is not marked dirty")
	return cmd
}

func newRepairDirtyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair-dirty",
		Short: "Repair a known dirty migration state or run up when already clean",
		RunE: func(cmd *cobra.Command, _ []string) error {
			confirmation, _ := cmd.Flags().GetString("confirm-force")
			if confirmation != "FORCE_MIGRATION_METADATA" {
				return fmt.Errorf("repair-dirty may mutate migration metadata; pass --confirm-force FORCE_MIGRATION_METADATA to continue")
			}
			expected, _ := cmd.Flags().GetString("expected-dirty-version")
			forceVersion, _ := cmd.Flags().GetString("force-version")
			thenUp, _ := cmd.Flags().GetBool("then-up")
			upIfClean, _ := cmd.Flags().GetBool("up-if-clean")

			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			repairer, ok := d.(repairDirtyDriver)
			if !ok {
				return fmt.Errorf("driver %q does not support repair-dirty", d.Name())
			}

			result, err := repairer.RepairDirty(context.Background(), req, golangmigrate.RepairDirtyOptions{
				ExpectedDirtyVersion: expected,
				ForceVersion:         forceVersion,
				ThenUp:               thenUp || upIfClean,
				UpIfClean:            upIfClean,
			})
			if err != nil {
				return fmt.Errorf("migrate repair-dirty: %w", err)
			}
			if thenUp || upIfClean {
				if len(result.Applied) == 0 {
					fmt.Printf("Repair-dirty completed; no pending migrations. Duration: %dms\n", result.DurationMs)
					return nil
				}
				fmt.Printf("Repair-dirty completed; applied %d migration(s): %v. Duration: %dms\n", len(result.Applied), result.Applied, result.DurationMs)
				return nil
			}
			fmt.Printf("Repaired dirty metadata at version %s to %s; no migrations applied. Duration: %dms\n", expected, forceVersion, result.DurationMs)
			return nil
		},
	}
	sharedFlags(cmd)
	cmd.Flags().String("expected-dirty-version", "", "Required dirty version currently recorded in migration metadata")
	cmd.Flags().String("force-version", "", "Version to force-set after the dirty version guard passes")
	cmd.Flags().String("confirm-force", "", "Typed confirmation required: FORCE_MIGRATION_METADATA")
	cmd.Flags().Bool("then-up", false, "Run pending migrations after successful metadata repair")
	cmd.Flags().Bool("up-if-clean", false, "Run normal up when the database is already clean; implies --then-up")
	_ = cmd.MarkFlagRequired("expected-dirty-version")
	_ = cmd.MarkFlagRequired("force-version")
	return cmd
}

func splitForceArgs(args []string) (string, []string, error) {
	var target string
	flagArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("force requires exactly one version")
			}
			if target != "" {
				return "", nil, fmt.Errorf("force requires exactly one version")
			}
			target = args[i+1]
			if i+2 < len(args) {
				flagArgs = append(flagArgs, args[i+2:]...)
			}
			break
		}
		if arg == "-1" || !strings.HasPrefix(arg, "-") {
			if target != "" {
				return "", nil, fmt.Errorf("force requires exactly one version")
			}
			target = arg
			continue
		}
		flagArgs = append(flagArgs, arg)
		if forceFlagNeedsValue(arg) {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("flag %s requires a value", arg)
			}
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	if target == "" {
		return "", nil, fmt.Errorf("force requires exactly one version")
	}
	return target, flagArgs, nil
}

func forceFlagNeedsValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	switch arg {
	case "--driver", "--source-dir", "--dsn", "--confirm-force":
		return true
	default:
		return false
	}
}
