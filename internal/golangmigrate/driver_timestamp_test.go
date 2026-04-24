package golangmigrate_test

// TestDriver_Up_TimestampVersions and TestDriver_Down_TimestampVersions are
// regression tests for the v0.3.0 panic in collectApplied() when migration
// versions are timestamp-based (e.g. 20240101000001) rather than sequential
// (1, 2, 3).
//
// Root cause: collectApplied(before=0, after=20240101000001) called
//   make([]string, 0, after-before)  with cap = 2×10¹³
// which panics: "makeslice: cap out of range".
//
// The Down() path had an equivalent bug with integer-range loops.
//
// These tests MUST PANIC on the pre-fix code and PASS after the fix.

import (
	"context"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
)

// timestampMigrations writes three pairs of up/down SQL files with
// BMW-style timestamp-based version numbers.
func timestampMigrations(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	writeSQL(t, dir, "20240101000001_init.up.sql",
		"CREATE TABLE users (id SERIAL PRIMARY KEY, email TEXT UNIQUE NOT NULL);")
	writeSQL(t, dir, "20240101000001_init.down.sql",
		"DROP TABLE IF EXISTS users;")

	writeSQL(t, dir, "20240201000001_posts.up.sql",
		"CREATE TABLE posts (id SERIAL PRIMARY KEY, user_id INT);")
	writeSQL(t, dir, "20240201000001_posts.down.sql",
		"DROP TABLE IF EXISTS posts;")

	writeSQL(t, dir, "20240301000001_comments.up.sql",
		"CREATE TABLE comments (id SERIAL PRIMARY KEY, post_id INT, body TEXT);")
	writeSQL(t, dir, "20240301000001_comments.down.sql",
		"DROP TABLE IF EXISTS comments;")

	return dir
}

// TestDriver_Up_TimestampVersions exercises Up() with timestamp-based migration
// versions — the exact scenario that triggered the v0.3.0 production panic.
//
// Pre-fix: panics with "makeslice: cap out of range" in collectApplied().
// Post-fix: applies 3 migrations, returns 3 version strings.
func TestDriver_Up_TimestampVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := timestampMigrations(t)

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN:    h.DSN(),
		Source: interfaces.MigrationSource{Dir: dir},
	}

	// Up: apply all 3 timestamp-versioned migrations.
	// PRE-FIX: panics with "makeslice: cap out of range".
	// POST-FIX: returns 3 applied version strings.
	result, err := d.Up(ctx, req)
	if err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Verify we got back meaningful applied versions (not an empty list).
	if len(result.Applied) == 0 {
		t.Error("Up(): expected applied migrations, got none")
	}

	// Each applied version string should be a timestamp-format number.
	for _, v := range result.Applied {
		if len(v) < 10 {
			t.Errorf("Up(): applied version %q looks too short — expected timestamp format like 20240101000001", v)
		}
	}

	// Status should reflect the highest version applied.
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.Current == "" {
		t.Error("Status().Current is empty after Up()")
	}
	if st.Current != "20240301000001" {
		t.Errorf("Status().Current = %q; want %q", st.Current, "20240301000001")
	}
	if len(st.Pending) != 0 {
		t.Errorf("Status().Pending = %v; want empty after full Up()", st.Pending)
	}

	t.Logf("Up() applied: %v", result.Applied)
	t.Logf("Status after Up: current=%s pending=%v", st.Current, st.Pending)
}

// TestDriver_Down_TimestampVersions exercises Down() with timestamp-based migration
// versions — the rollback equivalent of the v0.3.0 panic.
//
// Pre-fix: the integer-range loops at Down() lines 83-91 would iterate
// 2×10¹³ times, effectively hanging or OOM'ing before the makeslice panic.
// Post-fix: rolls back 1 migration, returns the rolled-back version string.
func TestDriver_Down_TimestampVersions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := timestampMigrations(t)

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN:    h.DSN(),
		Source: interfaces.MigrationSource{Dir: dir},
	}

	// Apply all migrations first (Up path must work for Down to be testable).
	if _, err := d.Up(ctx, req); err != nil {
		t.Fatalf("Up() prerequisite error: %v", err)
	}

	// Down: roll back 1 step.
	// PRE-FIX: hangs or panics with integer-range overflow.
	// POST-FIX: returns rolled-back version string.
	req.Options.Steps = 1
	result, err := d.Down(ctx, req)
	if err != nil {
		t.Fatalf("Down() error: %v", err)
	}

	if len(result.Applied) == 0 {
		t.Error("Down(): expected at least one rolled-back version, got none")
	}

	// The rolled-back version should be the highest one (20240301000001).
	if result.Applied[0] != "20240301000001" {
		t.Errorf("Down(): rolled-back version = %q; want %q", result.Applied[0], "20240301000001")
	}

	// Status should now show 20240201000001 as current.
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after Down() error: %v", err)
	}
	if st.Current != "20240201000001" {
		t.Errorf("Status().Current after Down() = %q; want %q", st.Current, "20240201000001")
	}

	t.Logf("Down() rolled back: %v", result.Applied)
	t.Logf("Status after Down: current=%s pending=%v", st.Current, st.Pending)
}

// TestDriver_Down_TimestampVersions_ToNil exercises Down() rolling back all
// migrations from a timestamp-versioned DB back to nil (clean state).
func TestDriver_Down_TimestampVersions_ToNil(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := timestampMigrations(t)

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN:    h.DSN(),
		Source: interfaces.MigrationSource{Dir: dir},
	}

	// Apply all.
	if _, err := d.Up(ctx, req); err != nil {
		t.Fatalf("Up() prerequisite error: %v", err)
	}

	// Roll back all 3 migrations.
	req.Options.Steps = 3
	result, err := d.Down(ctx, req)
	if err != nil {
		t.Fatalf("Down(steps=3) error: %v", err)
	}

	if len(result.Applied) == 0 {
		t.Error("Down(steps=3): expected rolled-back versions, got none")
	}

	t.Logf("Down(steps=3) rolled back: %v", result.Applied)
}
