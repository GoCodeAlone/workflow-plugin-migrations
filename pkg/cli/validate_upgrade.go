package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GoCodeAlone/workflow/interfaces"
)

func newValidateUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-upgrade",
		Short: "Validate baseline-to-candidate migration upgrade path",
		Long: `Validate an upgrade path by applying baseline migrations to an
empty configured database, then applying candidate migrations to the same database.

This catches failures that fresh-database migration tests miss, such as a
candidate migration that only fails when the current production schema already
contains the latest baseline migrations. The supplied database/schema must start
clean, with no user objects and no recorded migration version.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			baselineDir, _ := cmd.Flags().GetString("baseline-source-dir")
			if baselineDir == "" {
				return fmt.Errorf("--baseline-source-dir is required")
			}
			d, candidateReq, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			baselineReq := candidateReq
			baselineReq.Source = interfaces.MigrationSource{Dir: baselineDir}

			result, err := validateUpgrade(context.Background(), d, baselineReq, candidateReq)
			if err != nil {
				return err
			}
			fmt.Printf("Baseline applied %d migration(s): %v\n", len(result.BaselineApplied), result.BaselineApplied)
			fmt.Printf("Candidate applied %d migration(s): %v\n", len(result.CandidateApplied), result.CandidateApplied)
			fmt.Printf("Migration upgrade path valid. Current: %s\n", result.Current)
			return nil
		},
	}
	sharedFlags(cmd)
	cmd.Flags().String("baseline-source-dir", "", "Directory containing baseline migration files (required)")
	return cmd
}

var migrationFilenameVersionRe = regexp.MustCompile(`^(\d+)_.+\.sql$`)

type upgradeValidationResult struct {
	BaselineApplied  []string
	CandidateApplied []string
	Current          string
}

func validateUpgrade(ctx context.Context, d interfaces.MigrationDriver, baselineReq, candidateReq interfaces.MigrationRequest) (upgradeValidationResult, error) {
	return validateUpgradeWithSchemaCheck(ctx, d, baselineReq, candidateReq, ensureEmptySchema)
}

func validateUpgradeWithSchemaCheck(ctx context.Context, d interfaces.MigrationDriver, baselineReq, candidateReq interfaces.MigrationRequest, checkSchema func(context.Context, string) error) (upgradeValidationResult, error) {
	if err := checkSchema(ctx, baselineReq.DSN); err != nil {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade initial database check: %w", err)
	}
	initialStatus, err := d.Status(ctx, baselineReq)
	if err != nil {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade initial status: %w", err)
	}
	if initialStatus.Dirty {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade requires a clean empty database; initial state is dirty at version %s", initialStatus.Current)
	}
	if initialStatus.Current != "" {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade requires an empty database; initial current version is %s", initialStatus.Current)
	}

	baselineResult, err := d.Up(ctx, baselineReq)
	if err != nil {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade baseline up: %w", err)
	}
	baselineStatus, err := d.Status(ctx, baselineReq)
	if err != nil {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade baseline status: %w", err)
	}
	if baselineStatus.Dirty {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade baseline left database dirty at version %s", baselineStatus.Current)
	}
	if len(baselineStatus.Pending) > 0 {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade baseline has pending migrations after up: %v", baselineStatus.Pending)
	}
	if baselineStatus.Current != "" {
		if err := sourceContainsVersion(candidateReq.Source.Dir, baselineStatus.Current); err != nil {
			return upgradeValidationResult{}, fmt.Errorf("validate-upgrade candidate source consistency: %w", err)
		}
	}

	candidateResult, err := d.Up(ctx, candidateReq)
	if err != nil {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade candidate up: %w", err)
	}
	candidateStatus, err := d.Status(ctx, candidateReq)
	if err != nil {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade candidate status: %w", err)
	}
	if candidateStatus.Dirty {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade candidate left database dirty at version %s", candidateStatus.Current)
	}
	if len(candidateStatus.Pending) > 0 {
		return upgradeValidationResult{}, fmt.Errorf("validate-upgrade candidate has pending migrations after up: %v", candidateStatus.Pending)
	}

	return upgradeValidationResult{
		BaselineApplied:  baselineResult.Applied,
		CandidateApplied: candidateResult.Applied,
		Current:          candidateStatus.Current,
	}, nil
}

func ensureEmptySchema(ctx context.Context, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck

	var objectCount int
	err = db.QueryRowContext(ctx, `
SELECT count(*)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema()
  AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
  AND c.relname NOT IN ('schema_migrations', 'goose_db_version', 'atlas_schema_revisions')
`).Scan(&objectCount)
	if err != nil {
		return err
	}
	if objectCount > 0 {
		return fmt.Errorf("requires an empty schema; found %d existing user object(s)", objectCount)
	}
	return nil
}

func sourceContainsVersion(dir, version string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read source dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".down.sql") {
			continue
		}
		matches := migrationFilenameVersionRe.FindStringSubmatch(entry.Name())
		if len(matches) != 2 {
			continue
		}
		if versionsEqual(matches[1], version) {
			return nil
		}
	}
	return fmt.Errorf("source dir %s does not contain recorded baseline version %s", dir, version)
}

func versionsEqual(sourceVersion, recordedVersion string) bool {
	if sourceVersion == recordedVersion {
		return true
	}
	return strings.TrimLeft(sourceVersion, "0") == strings.TrimLeft(recordedVersion, "0")
}
