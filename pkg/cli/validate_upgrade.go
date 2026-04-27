package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GoCodeAlone/workflow/interfaces"
)

func newValidateUpgradeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate-upgrade",
		Short: "Validate baseline-to-candidate migration upgrade path",
		Long: `Validate an upgrade path by applying baseline migrations to the
configured database, then applying candidate migrations to the same database.

This catches failures that fresh-database migration tests miss, such as a
candidate migration that only fails when the current production schema already
contains the latest baseline migrations.`,
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

func sourceContainsVersion(dir, version string) error {
	want, err := strconv.ParseUint(version, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid recorded baseline version %q: %w", version, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read source dir %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		matches := migrationFilenameVersionRe.FindStringSubmatch(entry.Name())
		if len(matches) != 2 {
			continue
		}
		got, err := strconv.ParseUint(matches[1], 10, 64)
		if err == nil && got == want {
			return nil
		}
	}
	return fmt.Errorf("source dir %s does not contain recorded baseline version %s", dir, version)
}
