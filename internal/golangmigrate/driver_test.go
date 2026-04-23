package golangmigrate_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
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
}

func writeSQL(t *testing.T, dir, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
}
