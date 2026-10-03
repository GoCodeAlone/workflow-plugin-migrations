package cli

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/driver"
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

func TestTimeoutFlagOnlyOnRunnerCommands(t *testing.T) {
	root := NewRoot()
	for _, name := range []string{"up", "down", "status", "goto", "force", "repair-dirty", "validate-upgrade", "test"} {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			want := name == "up" || name == "down" || name == "status" || name == "goto"
			if got := cmd.Flags().Lookup("timeout") != nil; got != want {
				t.Fatalf("%s advertises timeout=%t; runner-backed=%t", name, got, want)
			}
		})
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
	_ = writeStatus(&out, interfaces.MigrationStatus{
		Current: "202604270001",
		Dirty:   false,
	})

	got := out.String()
	if !strings.Contains(got, "Dirty: false") {
		t.Fatalf("status output = %q; want explicit clean dirty flag", got)
	}

	out.Reset()
	_ = writeStatus(&out, interfaces.MigrationStatus{
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

// fakeUpDriver is a minimal MigrationDriver stub for CLI flag tests that
// don't require a real database. It returns a predetermined number of
// applied migrations from Up() and no-ops for all other operations.
type fakeUpDriver struct{ appliedCount int }

func (f *fakeUpDriver) Name() string { return "fake" }
func (f *fakeUpDriver) Up(_ context.Context, _ interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	applied := make([]string, f.appliedCount)
	for i := range applied {
		applied[i] = fmt.Sprintf("00000%d", i+1)
	}
	return interfaces.MigrationResult{Applied: applied}, nil
}
func (f *fakeUpDriver) Down(_ context.Context, _ interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	return interfaces.MigrationResult{}, nil
}
func (f *fakeUpDriver) Status(_ context.Context, _ interfaces.MigrationRequest) (interfaces.MigrationStatus, error) {
	return interfaces.MigrationStatus{}, nil
}
func (f *fakeUpDriver) Goto(_ context.Context, _ interfaces.MigrationRequest, _ string) (interfaces.MigrationResult, error) {
	return interfaces.MigrationResult{}, nil
}

func TestUpCmd_AcceptsUpIfCleanFlag(t *testing.T) {
	cmd := newUpCmd(driver.NewDefaultRegistry())
	flag := cmd.Flags().Lookup("up-if-clean")
	if flag == nil {
		t.Fatal("up command missing --up-if-clean flag")
	}
	if flag.DefValue != "false" {
		t.Errorf("up-if-clean default: got %q, want false", flag.DefValue)
	}
}

func TestUpCmd_UpIfCleanIsNoopWhenAlreadyClean(t *testing.T) {
	reg := driver.NewRegistry()
	reg.MustRegister(&fakeUpDriver{appliedCount: 0})
	cmd := newUpCmd(reg)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--driver", "fake", "--dsn", "postgres://local/db", "--up-if-clean", "--source-dir", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("up --up-if-clean against clean DB: got error %v, want nil", err)
	}
	if !strings.Contains(out.String(), "up-if-clean: no pending migrations") {
		t.Fatalf("injected output=%q", out.String())
	}
}

func TestUpCmd_UpIfCleanAppliesWhenPendingMigrationsExist(t *testing.T) {
	reg := driver.NewRegistry()
	reg.MustRegister(&fakeUpDriver{appliedCount: 2})
	cmd := newUpCmd(reg)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--driver", "fake", "--dsn", "postgres://local/db", "--up-if-clean", "--source-dir", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("up --up-if-clean with pending migrations: got error %v, want nil", err)
	}
	if !strings.Contains(out.String(), "Applied 2 migration(s)") {
		t.Fatalf("injected output=%q", out.String())
	}
}

func TestRootRunnerHonorsExecuteContext(t *testing.T) {
	for _, args := range [][]string{{"up"}, {"down"}, {"status"}, {"goto", "1"}} {
		t.Run(args[0], func(t *testing.T) {
			reg := driver.NewRegistry()
			reg.MustRegister(&fakeUpDriver{appliedCount: 1})
			root := newRoot(reg)
			var out, diagnostics bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&diagnostics)
			root.SetArgs(append(args, "--driver", "fake", "--dsn", "postgres://local/db", "--source-dir", t.TempDir()))
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("ExecuteContext=%v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("cancelled command output=%q", out.String())
			}
		})
	}
}

func TestRootRunnerRedactsErrorsAndUsesCommandOutput(t *testing.T) {
	for _, args := range [][]string{{"up"}, {"down"}, {"status"}, {"goto", "1"}} {
		t.Run(args[0], func(t *testing.T) {
			reg := driver.NewRegistry()
			reg.MustRegister(&fakeUpDriver{appliedCount: 1})
			root := newRoot(reg)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetArgs(append(args, "--driver", "fake", "--dsn", "postgres://local/db", "--source-dir", t.TempDir()))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if out.Len() == 0 {
				t.Fatal("command bypassed injected output")
			}
		})
	}
	root := NewRoot()
	var diagnostics bytes.Buffer
	root.SetErr(&diagnostics)
	root.SetArgs([]string{"up", "--dsn", "postgres://user:task15-private@localhost/db?connect_timeout=invalid", "--source-dir", t.TempDir()})
	err := root.Execute()
	if err == nil {
		t.Fatal("invalid connection accepted")
	}
	if strings.Contains(err.Error(), "task15-private") || strings.Contains(diagnostics.String(), "task15-private") {
		t.Fatal("CLI leaked DSN password")
	}
}

func TestCLIProviderAcceptsSDKCommandPrefix(t *testing.T) {
	args := []string{"db-migrate", "--help"}
	original := append([]string(nil), args...)
	if code := NewCLIProvider().RunCLI(args); code != 0 {
		t.Fatalf("SDK CLI command returned %d; want help success", code)
	}
	if !reflect.DeepEqual(args, original) {
		t.Fatal("CLI provider mutated caller arguments")
	}
}

func writeCLISQL(t *testing.T, dir, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o644); err != nil {
		t.Fatalf("write migration %s: %v", name, err)
	}
}
