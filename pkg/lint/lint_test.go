package lint_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/lint"
)

func TestLint_Clean(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "001_create_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	write(t, dir, "001_create_users.down.sql", "DROP TABLE IF EXISTS users;")
	write(t, dir, "002_add_email.up.sql", "ALTER TABLE users ADD COLUMN email TEXT;")
	write(t, dir, "002_add_email.down.sql", "ALTER TABLE users DROP COLUMN IF EXISTS email;")

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	if !r.Clean {
		t.Errorf("expected clean result, got issues: %+v", r.Issues)
	}
}

func TestLint_OrderingGap(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "001_create_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	write(t, dir, "001_create_users.down.sql", "DROP TABLE IF EXISTS users;")
	// skip 002
	write(t, dir, "003_add_email.up.sql", "ALTER TABLE users ADD COLUMN email TEXT;")
	write(t, dir, "003_add_email.down.sql", "ALTER TABLE users DROP COLUMN IF EXISTS email;")

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	assertIssue(t, r, "L001")
}

func TestLint_MissingDown(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "001_create_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	// no .down.sql

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	assertIssue(t, r, "L005")
}

func TestLint_NamingConvention(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "CreateUsers.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	assertIssue(t, r, "L003")
}

func TestLint_DangerousOp(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "001_drop_users.up.sql", "DROP TABLE users;") // no IF EXISTS
	write(t, dir, "001_drop_users.down.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	assertIssue(t, r, "L004")
}

func TestLint_DangerousOpSafe(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "001_drop_users.up.sql", "DROP TABLE IF EXISTS users;")
	write(t, dir, "001_drop_users.down.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	// L004 should NOT fire since IF EXISTS is present.
	for _, issue := range r.Issues {
		if issue.Code == "L004" {
			t.Errorf("unexpected L004 issue for guarded DROP TABLE IF EXISTS: %+v", issue)
		}
	}
}

func TestLint_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "001_create_users.up.sql", "")
	write(t, dir, "001_create_users.down.sql", "DROP TABLE IF EXISTS users;")

	r, err := lint.Lint(dir)
	if err != nil {
		t.Fatalf("Lint error: %v", err)
	}
	assertIssue(t, r, "L006")
}

func assertIssue(t *testing.T, r *lint.Result, code string) {
	t.Helper()
	for _, issue := range r.Issues {
		if issue.Code == code {
			return
		}
	}
	t.Errorf("expected issue %s not found in: %+v", code, r.Issues)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
