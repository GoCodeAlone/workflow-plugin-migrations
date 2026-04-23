// Package conformance provides a test suite that every MigrationDriver must pass.
// Third-party driver authors import this package and call Suite.Run(t, driver) to
// verify their implementation against the standard behavioural matrix.
package conformance

import (
	"context"
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
)

//go:embed corpus
var corpusFS embed.FS

// Suite runs the conformance test matrix against a given MigrationDriver.
type Suite struct {
	// DSN is the PostgreSQL connection string to use.
	DSN string
	// SourceDir is overridden per sub-test with a temp dir containing the corpus.
	sourceDir string
}

// NewSuite creates a Suite using the provided DSN.
func NewSuite(dsn string) *Suite {
	return &Suite{DSN: dsn}
}

// Run executes all conformance cases against the given driver.
func (s *Suite) Run(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	dir := s.extractCorpus(t)
	s.sourceDir = dir

	t.Run("FreshUpAll", func(t *testing.T) { s.testFreshUpAll(t, d) })
	t.Run("IdempotentUp", func(t *testing.T) { s.testIdempotentUp(t, d) })
	t.Run("DownOne", func(t *testing.T) { s.testDownOne(t, d) })
	t.Run("DownAll", func(t *testing.T) { s.testDownAll(t, d) })
	t.Run("GotoVersion", func(t *testing.T) { s.testGotoVersion(t, d) })
	t.Run("StatusReflectsState", func(t *testing.T) { s.testStatusReflectsState(t, d) })
}

// req builds a MigrationRequest for the suite's DSN and source dir.
func (s *Suite) req() interfaces.MigrationRequest {
	return interfaces.MigrationRequest{
		DSN:    s.DSN,
		Source: interfaces.MigrationSource{Dir: s.sourceDir},
	}
}

// testFreshUpAll asserts that Up applies all migrations on a fresh DB.
func (s *Suite) testFreshUpAll(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	ctx := context.Background()
	result, err := d.Up(ctx, s.req())
	if err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	if len(result.Applied) == 0 {
		t.Error("expected at least 1 applied migration")
	}

	st, err := d.Status(ctx, s.req())
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.Dirty {
		t.Error("unexpected dirty state after Up")
	}
	if len(st.Pending) > 0 {
		t.Errorf("unexpected pending migrations after Up: %v", st.Pending)
	}
}

// testIdempotentUp asserts that running Up again is a no-op.
func (s *Suite) testIdempotentUp(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	ctx := context.Background()

	// Apply all first.
	if _, err := d.Up(ctx, s.req()); err != nil {
		t.Fatalf("initial Up() error: %v", err)
	}

	// Re-run — should succeed with no new applied migrations.
	result, err := d.Up(ctx, s.req())
	if err != nil {
		t.Fatalf("idempotent Up() error: %v", err)
	}
	if len(result.Applied) > 0 {
		t.Errorf("idempotent Up applied %v; want none", result.Applied)
	}
}

// testDownOne asserts that Down rolls back exactly one migration.
func (s *Suite) testDownOne(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	ctx := context.Background()

	if _, err := d.Up(ctx, s.req()); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	stBefore, _ := d.Status(ctx, s.req())

	req := s.req()
	req.Options.Steps = 1
	if _, err := d.Down(ctx, req); err != nil {
		t.Fatalf("Down(1) error: %v", err)
	}

	stAfter, err := d.Status(ctx, s.req())
	if err != nil {
		t.Fatalf("Status() after Down error: %v", err)
	}
	if stAfter.Current == stBefore.Current {
		t.Error("Down did not change current version")
	}
}

// testDownAll asserts that repeated Down reaches an empty state.
func (s *Suite) testDownAll(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	ctx := context.Background()

	if _, err := d.Up(ctx, s.req()); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Roll back all migrations one at a time.
	for i := 0; i < 10; i++ {
		req := s.req()
		req.Options.Steps = 1
		_, err := d.Down(ctx, req)
		if err != nil {
			// No more migrations to roll back.
			break
		}
	}

	st, err := d.Status(ctx, s.req())
	if err != nil {
		t.Fatalf("Status() after DownAll error: %v", err)
	}
	if st.Current != "" && st.Current != "0" {
		t.Errorf("after DownAll current = %q; want empty or 0", st.Current)
	}
}

// testGotoVersion asserts that Goto moves the DB to the target version.
func (s *Suite) testGotoVersion(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	ctx := context.Background()

	if _, err := d.Up(ctx, s.req()); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Go to version 1.
	if _, err := d.Goto(ctx, s.req(), "1"); err != nil {
		t.Fatalf("Goto(1) error: %v", err)
	}

	st, err := d.Status(ctx, s.req())
	if err != nil {
		t.Fatalf("Status() after Goto error: %v", err)
	}
	if st.Current != "1" {
		t.Errorf("Goto(1): current = %q; want 1", st.Current)
	}
}

// testStatusReflectsState asserts that Status correctly reflects pending migrations.
func (s *Suite) testStatusReflectsState(t *testing.T, d interfaces.MigrationDriver) {
	t.Helper()
	ctx := context.Background()

	st, err := d.Status(ctx, s.req())
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	// On a fresh DB all migrations should be pending.
	if len(st.Pending) == 0 {
		t.Error("expected pending migrations on fresh DB")
	}
}

// extractCorpus copies embedded corpus SQL files to a temporary directory and
// returns its path.
func (s *Suite) extractCorpus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(corpusFS, "corpus", func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		data, err := corpusFS.ReadFile(path)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.Base(path))
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		t.Fatalf("extract corpus: %v", err)
	}
	return dir
}
