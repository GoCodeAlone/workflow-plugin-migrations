// Package atlas (internal test) — tests that directly exercise unexported
// seam variables and helpers in driver.go.
package atlas

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	atlmigrate "ariga.io/atlas/sql/migrate"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// TestRunWithRecover_ConvertsPanicToError verifies that runWithRecover
// converts a runtime panic (index out-of-range) into a wrapped error whose
// message contains both the phase name and the panic value.
func TestRunWithRecover_ConvertsPanicToError(t *testing.T) {
	err := runWithRecover("atlas-test-phase", func() error {
		a := []int{}
		_ = a[0] // intentional: runtime panic index out of range
		return nil
	})
	if err == nil {
		t.Fatal("runWithRecover: expected error from recovered panic, got nil")
	}
	if !strings.Contains(err.Error(), "atlas-test-phase panic") {
		t.Errorf("error should contain phase name 'atlas-test-phase panic'; got %v", err)
	}
	if !strings.Contains(err.Error(), "index out of range") {
		t.Errorf("error should describe the panic value; got %v", err)
	}
}

// TestRunWithRecover_NoErrorWhenNoPanic verifies the happy path: when the
// wrapped function succeeds without panicking, runWithRecover returns nil.
func TestRunWithRecover_NoErrorWhenNoPanic(t *testing.T) {
	err := runWithRecover("atlas-test-phase", func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("runWithRecover: expected nil error, got %v", err)
	}
}

// TestRunWithRecover_PropagatesNonPanicErrors verifies that when the wrapped
// function returns a normal (non-panic) error, runWithRecover propagates it.
func TestRunWithRecover_PropagatesNonPanicErrors(t *testing.T) {
	sentinel := fmt.Errorf("sentinel error")
	err := runWithRecover("atlas-test-phase", func() error {
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("runWithRecover: expected sentinel error, got %v", err)
	}
}

// TestUp_RecoversAtlasExecutorPanic verifies that Driver.Up returns a typed
// error (instead of crashing the process) when the atlas executor panics
// during ExecuteN. Uses two package-level seams to bypass real DB setup:
//   - openForTest: returns fake values so newSQLRevisionRW.init never runs
//   - newAtlasExecutorForTest: returns a panickingExecutor
func TestUp_RecoversAtlasExecutorPanic(t *testing.T) {
	// Seam out open() to bypass real DB setup.
	oldOpen := openForTest
	openForTest = func(_ interfaces.MigrationRequest) (*sql.DB, *atlmigrate.LocalDir, *sqlRevisionRW, atlmigrate.Driver, func(), error) {
		return nil, nil, &sqlRevisionRW{}, nil, func() {}, nil
	}
	defer func() { openForTest = oldOpen }()

	// Inject a panicking executor.
	oldNew := newAtlasExecutorForTest
	newAtlasExecutorForTest = func(_ atlmigrate.Driver, _ atlmigrate.Dir, _ atlmigrate.RevisionReadWriter, _ ...atlmigrate.ExecutorOption) (atlasExecutor, error) {
		return &panickingExecutor{}, nil
	}
	defer func() { newAtlasExecutorForTest = oldNew }()

	d := &Driver{}
	_, err := d.Up(context.Background(), interfaces.MigrationRequest{
		DSN:    "postgres://fake-for-test",
		Source: interfaces.MigrationSource{Dir: "/fake-for-test"},
	})
	if err == nil {
		t.Fatal("Up: expected wrapped error from recovered atlas panic, got nil")
	}
	if !strings.Contains(err.Error(), "atlas-execute panic") {
		t.Errorf("Up: error should mention 'atlas-execute panic'; got %v", err)
	}
	if !strings.Contains(err.Error(), "index out of range") {
		t.Errorf("Up: error should describe panic value; got %v", err)
	}
}

// panickingExecutor implements atlasExecutor and intentionally panics on
// ExecuteN to simulate the upstream atlas panic seen in workflow#513.
type panickingExecutor struct{}

func (p *panickingExecutor) ExecuteN(_ context.Context, _ int) error {
	a := []int{}
	_ = a[0] // intentional: index out of range panic
	return nil
}

func (p *panickingExecutor) Pending(_ context.Context) ([]atlmigrate.File, error) {
	return nil, nil
}
