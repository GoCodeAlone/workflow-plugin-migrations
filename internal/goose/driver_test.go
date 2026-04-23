package goose_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"

	goosedriver "github.com/GoCodeAlone/workflow-plugin-migrations/internal/goose"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
)

func TestGooseDriver_Name(t *testing.T) {
	d := goosedriver.New()
	if got := d.Name(); got != "goose" {
		t.Errorf("Name() = %q; want %q", got, "goose")
	}
}

func TestGooseDriver_UpDownStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := t.TempDir()
	writeGooseSQL(t, dir, "00001_users.sql", `-- +goose Up
CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);
-- +goose Down
DROP TABLE IF EXISTS users;
`)
	writeGooseSQL(t, dir, "00002_posts.sql", `-- +goose Up
CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);
-- +goose Down
DROP TABLE IF EXISTS posts;
`)

	ctx := context.Background()
	d := goosedriver.New()
	req := interfaces.MigrationRequest{
		DSN: h.DSN(),
		Source: interfaces.MigrationSource{
			Dir: dir,
		},
	}

	// Status: no migrations applied.
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if st.Dirty {
		t.Error("expected clean state")
	}

	// Up: apply all.
	result, err := d.Up(ctx, req)
	if err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Errorf("Applied = %v; want 2 migrations", result.Applied)
	}

	// Status: current = 2.
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after up: %v", err)
	}
	if st.Current != "2" {
		t.Errorf("Current = %q; want 2", st.Current)
	}

	// Down: roll back 1.
	req.Options.Steps = 1
	_, err = d.Down(ctx, req)
	if err != nil {
		t.Fatalf("Down() error: %v", err)
	}

	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after down: %v", err)
	}
	if st.Current != "1" {
		t.Errorf("Current after down = %q; want 1", st.Current)
	}
}

func writeGooseSQL(t *testing.T, dir, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o644); err != nil {
		t.Fatal(err)
	}
}
