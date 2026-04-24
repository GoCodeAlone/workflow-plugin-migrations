package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/conformance"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Run full-cycle and checkpoint migration tests",
		Long: `Run migration conformance tests against the configured database.

Verifies Up/Down/Status correctness with two scenarios:
  full-cycle:  Up all → assert no pending → Down all → assert clean state
  checkpoint:  Up all → Down 1 → assert 1 pending and current changed

Use --keep-alive to apply all migrations and print DSN on stdout for
integration test setup (the calling process then uses that DB).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, req, err := buildDriverAndRequest(cmd)
			if err != nil {
				return err
			}
			keepAlive, _ := cmd.Flags().GetBool("keep-alive")
			ctx := context.Background()
			runner := conformance.NewRunner(req.DSN, req.Source.Dir, d)

			if keepAlive {
				if err := runner.Setup(ctx); err != nil {
					return fmt.Errorf("migrate test --keep-alive: %w", err)
				}
				fmt.Fprintln(os.Stdout, req.DSN) //nolint:errcheck
				return nil
			}

			results := runner.Run(ctx)
			pass, fail := 0, 0
			for _, r := range results {
				if r.Err != nil {
					fmt.Fprintf(os.Stderr, "FAIL  %s: %v\n", r.Name, r.Err)
					fail++
				} else {
					fmt.Printf("PASS  %s\n", r.Name)
					pass++
				}
			}
			fmt.Printf("\n%d passed, %d failed\n", pass, fail)
			if fail > 0 {
				return fmt.Errorf("migration tests: %d failure(s)", fail)
			}
			return nil
		},
	}
	sharedFlags(cmd)
	cmd.Flags().Bool("keep-alive", false,
		"Apply all migrations, print DSN on stdout, and exit 0 (for integration test setup)")
	return cmd
}
