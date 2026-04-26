package cli

import (
	"strings"
	"testing"
)

func TestRootIncludesForceCommand(t *testing.T) {
	root := NewRoot()

	cmd, _, err := root.Find([]string{"force", "1"})
	if err != nil {
		t.Fatalf("Find(force) error: %v", err)
	}
	if cmd == nil || cmd.Name() != "force" {
		t.Fatalf("Find(force) = %v; want force command", cmd)
	}
}

func TestForceCommandRequiresTypedConfirmation(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{
		"force",
		"1",
		"--source-dir", t.TempDir(),
		"--dsn", "postgres://user:pass@example.invalid/db",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want confirmation error")
	}
	if !strings.Contains(err.Error(), "--confirm-force FORCE_MIGRATION_METADATA") {
		t.Fatalf("Execute() error = %v; want confirmation error", err)
	}
}

func TestForceCommandRejectsInvalidVersionWithConfirmation(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{
		"force",
		"not-a-version",
		"--source-dir", t.TempDir(),
		"--dsn", "postgres://user:pass@example.invalid/db",
		"--confirm-force", "FORCE_MIGRATION_METADATA",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want invalid version error")
	}
	if !strings.Contains(err.Error(), "invalid target version") {
		t.Fatalf("Execute() error = %v; want invalid target version", err)
	}
}

func TestForceCommandAcceptsNegativeNilVersionAsArgument(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{
		"force",
		"-1",
		"--source-dir", t.TempDir(),
		"--dsn", "postgres://user:pass@example.invalid/db",
		"--confirm-force", "FORCE_MIGRATION_METADATA",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want database connection error after parsing -1 as argument")
	}
	if strings.Contains(err.Error(), "unknown shorthand flag") {
		t.Fatalf("Execute() error = %v; -1 was parsed as a flag", err)
	}
	if strings.Contains(err.Error(), "invalid target version") {
		t.Fatalf("Execute() error = %v; -1 should be a valid nil-version target", err)
	}
}
