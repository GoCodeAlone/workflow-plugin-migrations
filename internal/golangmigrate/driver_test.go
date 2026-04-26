package golangmigrate_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestDriver_Name(t *testing.T) {
	d := golangmigrate.New()
	if got := d.Name(); got != "golang-migrate" {
		t.Errorf("Name() = %q; want %q", got, "golang-migrate")
	}
}

func TestDriver_UpDownStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := t.TempDir()
	writeSQL(t, dir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	writeSQL(t, dir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")
	writeSQL(t, dir, "000002_posts.up.sql", "CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);")
	writeSQL(t, dir, "000002_posts.down.sql", "DROP TABLE IF EXISTS posts;")

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN: h.DSN(),
		Source: interfaces.MigrationSource{
			Dir: dir,
		},
	}

	// Status: no migrations applied yet.
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.Dirty {
		t.Error("expected clean state before migrations")
	}

	// Up: apply all.
	result, err := d.Up(ctx, req)
	if err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	if len(result.Applied) == 0 {
		t.Error("expected at least one applied migration")
	}

	// Status: current should be 2.
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after up error: %v", err)
	}
	if st.Current != "2" {
		t.Errorf("Current = %q; want %q", st.Current, "2")
	}

	// Down: roll back 1.
	req.Options.Steps = 1
	_, err = d.Down(ctx, req)
	if err != nil {
		t.Fatalf("Down() error: %v", err)
	}

	// Status: current should be 1.
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after down error: %v", err)
	}
	if st.Current != "1" {
		t.Errorf("Current = %q; want %q", st.Current, "1")
	}

	// Goto: back to 2.
	_, err = d.Goto(ctx, req, "2")
	if err != nil {
		t.Fatalf("Goto() error: %v", err)
	}
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after goto error: %v", err)
	}
	if st.Current != "2" {
		t.Errorf("Goto: Current = %q; want %q", st.Current, "2")
	}

	// Force refuses clean databases by default.
	_, err = d.Force(ctx, req, "1", golangmigrate.ForceOptions{})
	if err == nil {
		t.Fatal("Force() error = nil; want clean database refusal")
	}
	if !strings.Contains(err.Error(), "database is clean") {
		t.Fatalf("Force() error = %v; want clean database refusal", err)
	}

	markDirty(t, h.DSN(), 2)

	// Force: set the recorded version without running migrations.
	result, err = d.Force(ctx, req, "1", golangmigrate.ForceOptions{})
	if err != nil {
		t.Fatalf("Force() error: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("Force() Applied = %v; force must not report applied migrations", result.Applied)
	}
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after force error: %v", err)
	}
	if st.Current != "1" {
		t.Errorf("Force: Current = %q; want %q", st.Current, "1")
	}
	if st.Dirty {
		t.Error("Force: expected clean state")
	}

	markDirty(t, h.DSN(), 1)
	_, err = d.Force(ctx, req, "999", golangmigrate.ForceOptions{})
	if err == nil {
		t.Fatal("Force() missing target error = nil; want error")
	}
	if !strings.Contains(err.Error(), "does not exist in migration source") {
		t.Fatalf("Force() missing target error = %v; want missing target", err)
	}

	_, err = d.Force(ctx, req, "-1", golangmigrate.ForceOptions{})
	if err != nil {
		t.Fatalf("Force(-1) error: %v", err)
	}
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after force -1 error: %v", err)
	}
	if st.Current != "" {
		t.Errorf("Force(-1): Current = %q; want nil version", st.Current)
	}
	if st.Dirty {
		t.Error("Force(-1): expected clean state")
	}
}

func TestDriver_ForceRejectsInvalidTarget(t *testing.T) {
	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN: "postgres://user:pass@example.invalid/db",
		Source: interfaces.MigrationSource{
			Dir: t.TempDir(),
		},
	}

	for _, target := range []string{"", "-2", "0", "abc", "1.5"} {
		t.Run(target, func(t *testing.T) {
			_, err := d.Force(ctx, req, target, golangmigrate.ForceOptions{})
			if err == nil {
				t.Fatal("Force() error = nil; want invalid target error")
			}
			if !strings.Contains(err.Error(), "invalid target version") {
				t.Fatalf("Force() error = %v; want invalid target version", err)
			}
		})
	}
}

func TestDriver_RepairDirtyGuardsExpectedVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := t.TempDir()
	writeSQL(t, dir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	writeSQL(t, dir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")
	writeSQL(t, dir, "000002_posts.up.sql", "CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);")
	writeSQL(t, dir, "000002_posts.down.sql", "DROP TABLE IF EXISTS posts;")
	writeSQL(t, dir, "000003_comments.up.sql", "CREATE TABLE comments (id SERIAL PRIMARY KEY, body TEXT NOT NULL);")
	writeSQL(t, dir, "000003_comments.down.sql", "DROP TABLE IF EXISTS comments;")

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN: h.DSN(),
		Source: interfaces.MigrationSource{
			Dir: dir,
		},
	}

	if _, err := d.Goto(ctx, req, "1"); err != nil {
		t.Fatalf("Goto(1) error: %v", err)
	}

	_, err = d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{
		ExpectedDirtyVersion: "2",
		ForceVersion:         "1",
	})
	if err == nil {
		t.Fatal("RepairDirty() clean error = nil; want refusal")
	}
	if !strings.Contains(err.Error(), "database is clean") {
		t.Fatalf("RepairDirty() clean error = %v; want clean refusal", err)
	}

	markDirty(t, h.DSN(), 2)

	_, err = d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{
		ExpectedDirtyVersion: "3",
		ForceVersion:         "1",
	})
	if err == nil {
		t.Fatal("RepairDirty() wrong-version error = nil; want refusal")
	}
	if !strings.Contains(err.Error(), "dirty at version 2, expected 3") {
		t.Fatalf("RepairDirty() wrong-version error = %v; want exact-version refusal", err)
	}

	_, err = d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{
		ExpectedDirtyVersion: "2",
		ForceVersion:         "3",
	})
	if err == nil {
		t.Fatal("RepairDirty() forward-force error = nil; want refusal")
	}
	if !strings.Contains(err.Error(), "must not be greater than expected dirty version") {
		t.Fatalf("RepairDirty() forward-force error = %v; want forward-force refusal", err)
	}

	result, err := d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{
		ExpectedDirtyVersion: "2",
		ForceVersion:         "1",
	})
	if err != nil {
		t.Fatalf("RepairDirty() error: %v", err)
	}
	if len(result.Applied) != 0 {
		t.Fatalf("RepairDirty() Applied = %v; metadata repair must not report applied migrations", result.Applied)
	}
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after RepairDirty error: %v", err)
	}
	if st.Current != "1" || st.Dirty {
		t.Fatalf("Status() after RepairDirty = current %q dirty %v; want current 1 clean", st.Current, st.Dirty)
	}
}

func TestDriver_RepairDirtyThenUp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := t.TempDir()
	writeSQL(t, dir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	writeSQL(t, dir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")
	writeSQL(t, dir, "000002_posts.up.sql", "CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);")
	writeSQL(t, dir, "000002_posts.down.sql", "DROP TABLE IF EXISTS posts;")

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN: h.DSN(),
		Source: interfaces.MigrationSource{
			Dir: dir,
		},
	}
	if _, err := d.Goto(ctx, req, "1"); err != nil {
		t.Fatalf("Goto(1) error: %v", err)
	}
	markDirty(t, h.DSN(), 2)

	result, err := d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{
		ExpectedDirtyVersion: "2",
		ForceVersion:         "1",
		ThenUp:               true,
	})
	if err != nil {
		t.Fatalf("RepairDirty(ThenUp) error: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "2" {
		t.Fatalf("RepairDirty(ThenUp) Applied = %v; want [2]", result.Applied)
	}
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after RepairDirty(ThenUp) error: %v", err)
	}
	if st.Current != "2" || st.Dirty {
		t.Fatalf("Status() after RepairDirty(ThenUp) = current %q dirty %v; want current 2 clean", st.Current, st.Dirty)
	}
}

func TestDriver_RepairDirtyThenUpReportsPartialRepairOnUpFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := t.TempDir()
	writeSQL(t, dir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	writeSQL(t, dir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")
	writeSQL(t, dir, "000002_posts.up.sql", "CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);")
	writeSQL(t, dir, "000002_posts.down.sql", "DROP TABLE IF EXISTS posts;")
	writeSQL(t, dir, "000003_broken.up.sql", "THIS IS NOT SQL;")
	writeSQL(t, dir, "000003_broken.down.sql", "SELECT 1;")

	ctx := context.Background()
	d := golangmigrate.New()
	req := interfaces.MigrationRequest{
		DSN: h.DSN(),
		Source: interfaces.MigrationSource{
			Dir: dir,
		},
	}
	if _, err := d.Goto(ctx, req, "1"); err != nil {
		t.Fatalf("Goto(1) error: %v", err)
	}
	markDirty(t, h.DSN(), 2)

	_, err = d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{
		ExpectedDirtyVersion: "2",
		ForceVersion:         "1",
		ThenUp:               true,
	})
	if err == nil {
		t.Fatal("RepairDirty(ThenUp) error = nil; want broken migration failure")
	}
	if !strings.Contains(err.Error(), "then-up failed after metadata was repaired to version 1") {
		t.Fatalf("RepairDirty(ThenUp) error = %v; want partial-repair message", err)
	}
	if !strings.Contains(err.Error(), "current version 3 dirty=true") {
		t.Fatalf("RepairDirty(ThenUp) error = %v; want final dirty status", err)
	}
}

func markDirty(t *testing.T, dsn string, version int) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	if _, err := db.Exec(`UPDATE schema_migrations SET version = $1, dirty = true`, version); err != nil {
		t.Fatalf("mark schema_migrations dirty: %v", err)
	}
}

func writeSQL(t *testing.T, dir, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
}
