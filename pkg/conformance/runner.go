package conformance

import (
	"context"
	"fmt"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// CaseResult holds the outcome of a single conformance case.
type CaseResult struct {
	Name string
	Err  error
}

// Runner executes the conformance suite without requiring *testing.T.
// It is used by the `migrate test` CLI subcommand and can be embedded in
// integration-test harnesses that need pass/fail results as Go values.
type Runner struct {
	dsn    string
	dir    string
	driver interfaces.MigrationDriver
}

// NewRunner creates a Runner for the given driver, DSN, and migration directory.
func NewRunner(dsn, dir string, d interfaces.MigrationDriver) *Runner {
	return &Runner{dsn: dsn, dir: dir, driver: d}
}

// Setup applies all pending migrations. Called by `migrate test --keep-alive` to
// prepare the database for external integration tests and print the DSN.
func (r *Runner) Setup(ctx context.Context) error {
	_, err := r.driver.Up(ctx, r.req())
	return err
}

// Run executes the full-cycle and checkpoint conformance cases sequentially.
// Each case resets the database state (via repeated Down calls) before running.
func (r *Runner) Run(ctx context.Context) []CaseResult {
	cases := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"FullCycle", r.runFullCycle},
		{"Checkpoint", r.runCheckpoint},
	}

	results := make([]CaseResult, 0, len(cases))
	for _, c := range cases {
		r.reset(ctx) // best-effort reset before each case
		err := c.fn(ctx)
		results = append(results, CaseResult{Name: c.name, Err: err})
	}
	return results
}

func (r *Runner) req() interfaces.MigrationRequest {
	return interfaces.MigrationRequest{
		DSN:    r.dsn,
		Source: interfaces.MigrationSource{Dir: r.dir},
	}
}

// reset rolls back all applied migrations to restore a clean state.
// It ignores errors (the DB may already be clean) and caps iterations
// to prevent infinite loops.
func (r *Runner) reset(ctx context.Context) {
	for i := 0; i < 100; i++ {
		req := r.req()
		req.Options.Steps = 1
		_, err := r.driver.Down(ctx, req)
		if err != nil {
			return
		}
		st, err := r.driver.Status(ctx, r.req())
		if err != nil || st.Current == "" || st.Current == "0" {
			return
		}
	}
}

// runFullCycle: Up all → assert no pending/not dirty → Down all → assert clean.
func (r *Runner) runFullCycle(ctx context.Context) error {
	// Apply all migrations.
	result, err := r.driver.Up(ctx, r.req())
	if err != nil {
		return fmt.Errorf("full-cycle Up: %w", err)
	}
	if len(result.Applied) == 0 {
		return fmt.Errorf("full-cycle Up: expected at least one migration applied on fresh DB")
	}

	// Assert nothing pending and not dirty.
	st, err := r.driver.Status(ctx, r.req())
	if err != nil {
		return fmt.Errorf("full-cycle Status after Up: %w", err)
	}
	if len(st.Pending) > 0 {
		return fmt.Errorf("full-cycle: unexpected pending migrations after Up: %v", st.Pending)
	}
	if st.Dirty {
		return fmt.Errorf("full-cycle: dirty state after Up")
	}

	// Roll back all migrations.
	r.reset(ctx)

	// Assert clean state.
	st, err = r.driver.Status(ctx, r.req())
	if err != nil {
		return fmt.Errorf("full-cycle Status after Down all: %w", err)
	}
	if st.Current != "" && st.Current != "0" {
		return fmt.Errorf("full-cycle: expected empty current after Down all, got %q", st.Current)
	}
	return nil
}

// runCheckpoint: Up all → Down 1 → assert current changed and 1 pending.
func (r *Runner) runCheckpoint(ctx context.Context) error {
	// Apply all migrations.
	if _, err := r.driver.Up(ctx, r.req()); err != nil {
		return fmt.Errorf("checkpoint Up: %w", err)
	}

	stFull, err := r.driver.Status(ctx, r.req())
	if err != nil {
		return fmt.Errorf("checkpoint Status after Up: %w", err)
	}
	currentBefore := stFull.Current

	// Roll back one migration.
	req := r.req()
	req.Options.Steps = 1
	if _, err := r.driver.Down(ctx, req); err != nil {
		return fmt.Errorf("checkpoint Down(1): %w", err)
	}

	// Assert current changed and exactly 1 pending.
	st, err := r.driver.Status(ctx, r.req())
	if err != nil {
		return fmt.Errorf("checkpoint Status after Down(1): %w", err)
	}
	if st.Current == currentBefore {
		return fmt.Errorf("checkpoint: current version unchanged after Down(1): %q", st.Current)
	}
	if len(st.Pending) == 0 {
		return fmt.Errorf("checkpoint: expected at least 1 pending migration after Down(1)")
	}
	return nil
}
