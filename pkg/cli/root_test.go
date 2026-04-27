package cli

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
	"github.com/GoCodeAlone/workflow/interfaces"
	_ "github.com/jackc/pgx/v5/stdlib"
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

func TestRootIncludesRepairDirtyCommand(t *testing.T) {
	root := NewRoot()

	cmd, _, err := root.Find([]string{"repair-dirty"})
	if err != nil {
		t.Fatalf("Find(repair-dirty) error: %v", err)
	}
	if cmd == nil || cmd.Name() != "repair-dirty" {
		t.Fatalf("Find(repair-dirty) = %v; want repair-dirty command", cmd)
	}
	if flag := cmd.Flags().Lookup("up-if-clean"); flag == nil {
		t.Fatal("repair-dirty --up-if-clean flag is missing")
	} else if !strings.Contains(flag.Usage, "implies --then-up") {
		t.Fatalf("--up-if-clean usage = %q; want then-up implication documented", flag.Usage)
	}
}

func TestWriteStatusIncludesExplicitDirtyFlag(t *testing.T) {
	var out bytes.Buffer
	writeStatus(&out, interfaces.MigrationStatus{
		Current: "202604270001",
		Dirty:   false,
	})

	got := out.String()
	if !strings.Contains(got, "Dirty: false") {
		t.Fatalf("status output = %q; want explicit clean dirty flag", got)
	}

	out.Reset()
	writeStatus(&out, interfaces.MigrationStatus{
		Current: "202604270001",
		Dirty:   true,
	})
	got = out.String()
	if !strings.Contains(got, "Dirty: true") || !strings.Contains(got, "WARNING: database is in dirty state!") {
		t.Fatalf("status output = %q; want explicit dirty flag and warning", got)
	}
}

func TestRootIncludesValidateUpgradeCommand(t *testing.T) {
	root := NewRoot()

	cmd, _, err := root.Find([]string{"validate-upgrade"})
	if err != nil {
		t.Fatalf("Find(validate-upgrade) error: %v", err)
	}
	if cmd == nil || cmd.Name() != "validate-upgrade" {
		t.Fatalf("Find(validate-upgrade) = %v; want validate-upgrade command", cmd)
	}
	if flag := cmd.Flags().Lookup("baseline-source-dir"); flag == nil {
		t.Fatal("validate-upgrade --baseline-source-dir flag is missing")
	}
}

func TestValidateUpgradeCommandRequiresBaselineSourceDirBeforeConnecting(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{
		"validate-upgrade",
		"--source-dir", t.TempDir(),
		"--dsn", "postgres://user:pass@example.invalid/db",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want baseline source dir error")
	}
	if !strings.Contains(err.Error(), "--baseline-source-dir is required") {
		t.Fatalf("Execute() error = %v; want baseline source dir error", err)
	}
}

func TestValidateUpgradeCommandAppliesBaselineThenCandidate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	baselineDir := t.TempDir()
	writeCLISQL(t, baselineDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	writeCLISQL(t, baselineDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")

	candidateDir := t.TempDir()
	writeCLISQL(t, candidateDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);")
	writeCLISQL(t, candidateDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")
	writeCLISQL(t, candidateDir, "000002_users_email.up.sql", "ALTER TABLE users ADD COLUMN email TEXT;")
	writeCLISQL(t, candidateDir, "000002_users_email.down.sql", "ALTER TABLE users DROP COLUMN IF EXISTS email;")

	root := NewRoot()
	root.SetArgs([]string{
		"validate-upgrade",
		"--baseline-source-dir", baselineDir,
		"--source-dir", candidateDir,
		"--dsn", h.DSN(),
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	db, err := sql.Open("pgx", h.DSN())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck

	var columnCount int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'email'`).Scan(&columnCount); err != nil {
		t.Fatalf("query users.email column: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("users.email column count = %d; want 1", columnCount)
	}
}

func TestValidateUpgradeCommandFailsDirtyCandidateMigration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	baselineDir := t.TempDir()
	writeCLISQL(t, baselineDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	writeCLISQL(t, baselineDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")

	candidateDir := t.TempDir()
	writeCLISQL(t, candidateDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	writeCLISQL(t, candidateDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")
	writeCLISQL(t, candidateDir, "000002_bad.up.sql", "ALTER TABLE missing_table ADD COLUMN email TEXT;")
	writeCLISQL(t, candidateDir, "000002_bad.down.sql", "ALTER TABLE missing_table DROP COLUMN IF EXISTS email;")

	root := NewRoot()
	root.SetArgs([]string{
		"validate-upgrade",
		"--baseline-source-dir", baselineDir,
		"--source-dir", candidateDir,
		"--dsn", h.DSN(),
	})
	err = root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want candidate migration failure")
	}
	if !strings.Contains(err.Error(), "validate-upgrade candidate up") {
		t.Fatalf("Execute() error = %v; want candidate up context", err)
	}
}

func TestValidateUpgradeCommandRejectsNonEmptySchemaWithoutMigrationMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres (set up with testharness)")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	db, err := sql.Open("pgx", h.DSN())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	if _, err := db.Exec(`CREATE TABLE preexisting_object (id integer)`); err != nil {
		t.Fatalf("create preexisting object: %v", err)
	}

	baselineDir := t.TempDir()
	writeCLISQL(t, baselineDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	writeCLISQL(t, baselineDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")

	root := NewRoot()
	root.SetArgs([]string{
		"validate-upgrade",
		"--baseline-source-dir", baselineDir,
		"--source-dir", baselineDir,
		"--dsn", h.DSN(),
	})
	err = root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want non-empty schema rejection")
	}
	if !strings.Contains(err.Error(), "requires an empty schema") {
		t.Fatalf("Execute() error = %v; want empty schema rejection", err)
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

func TestRepairDirtyCommandRequiresTypedConfirmation(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{
		"repair-dirty",
		"--expected-dirty-version", "2",
		"--force-version", "1",
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

func TestRepairDirtyCommandRejectsInvalidVersionsBeforeConnecting(t *testing.T) {
	tests := []struct {
		name              string
		expected          string
		forceVersion      string
		wantErrorContains string
	}{
		{
			name:              "expected",
			expected:          "not-a-version",
			forceVersion:      "1",
			wantErrorContains: "invalid expected dirty version",
		},
		{
			name:              "force",
			expected:          "2",
			forceVersion:      "not-a-version",
			wantErrorContains: "golang-migrate repair-dirty: invalid target version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := NewRoot()
			root.SetArgs([]string{
				"repair-dirty",
				"--expected-dirty-version", tt.expected,
				"--force-version", tt.forceVersion,
				"--source-dir", t.TempDir(),
				"--dsn", "postgres://user:pass@example.invalid/db",
				"--confirm-force", "FORCE_MIGRATION_METADATA",
			})

			err := root.Execute()
			if err == nil {
				t.Fatal("Execute() error = nil; want invalid version error")
			}
			if !strings.Contains(err.Error(), tt.wantErrorContains) {
				t.Fatalf("Execute() error = %v; want %q", err, tt.wantErrorContains)
			}
		})
	}
}

func TestRepairDirtyCommandRejectsUnsupportedDrivers(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{
		"repair-dirty",
		"--driver", "goose",
		"--expected-dirty-version", "2",
		"--force-version", "1",
		"--up-if-clean",
		"--source-dir", t.TempDir(),
		"--dsn", "postgres://user:pass@example.invalid/db",
		"--confirm-force", "FORCE_MIGRATION_METADATA",
	})

	err := root.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil; want unsupported driver error")
	}
	if !strings.Contains(err.Error(), `driver "goose" does not support repair-dirty`) {
		t.Fatalf("Execute() error = %v; want unsupported driver error", err)
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

func writeCLISQL(t *testing.T, dir, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o644); err != nil {
		t.Fatalf("write migration %s: %v", name, err)
	}
}
