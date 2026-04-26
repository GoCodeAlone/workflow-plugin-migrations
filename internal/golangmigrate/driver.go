// Package golangmigrate provides a MigrationDriver backed by golang-migrate/migrate/v4.
package golangmigrate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	migratefile "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// Driver implements interfaces.MigrationDriver using golang-migrate.
type Driver struct{}

// New returns a new golang-migrate Driver.
func New() *Driver { return &Driver{} }

// Name returns the driver name.
func (d *Driver) Name() string { return "golang-migrate" }

// Up applies all pending migrations.
func (d *Driver) Up(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	m, err := newMigrate(req)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: %w", err)
	}
	defer m.Close() //nolint:errcheck

	// Capture the version before applying; fail fast if the DB is unavailable.
	before, _, beforeErr := m.Version()
	if beforeErr != nil && !errors.Is(beforeErr, migrate.ErrNilVersion) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: version before up: %w", beforeErr)
	}
	atNilBefore := errors.Is(beforeErr, migrate.ErrNilVersion)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate up: %w", err)
	}

	after, _, afterErr := m.Version()
	if afterErr != nil && !errors.Is(afterErr, migrate.ErrNilVersion) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: version after up: %w", afterErr)
	}

	// Walk the source directory to enumerate applied versions; this is safe
	// for timestamp-based version numbers where after-before can be 10^13.
	applied, err := versionsInRange(req.Source.Dir, before, after, atNilBefore)
	if err != nil {
		// Migration succeeded but we can't enumerate applied versions — log and
		// return partial info rather than hiding the applied state entirely.
		log.Printf("warn: golang-migrate: applied version enumeration failed: %v", err)
		applied = nil
	}

	return interfaces.MigrationResult{
		Applied:    applied,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Down rolls back N migrations (Options.Steps, default 1).
func (d *Driver) Down(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	m, err := newMigrate(req)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: %w", err)
	}
	defer m.Close() //nolint:errcheck

	steps := req.Options.Steps
	if steps <= 0 {
		steps = 1
	}

	before, _, beforeErr := m.Version()
	if beforeErr != nil && !errors.Is(beforeErr, migrate.ErrNilVersion) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: version before down: %w", beforeErr)
	}

	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate down: %w", err)
	}

	after, _, afterErr := m.Version()
	if afterErr != nil && !errors.Is(afterErr, migrate.ErrNilVersion) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: version after down: %w", afterErr)
	}

	// Build list of rolled-back version strings (highest to lowest).
	// Walk the source directory instead of integer-range loops — safe for
	// timestamp-based version numbers where before-after can be 10^13.
	var rolledBack []string
	afterNil := errors.Is(afterErr, migrate.ErrNilVersion)
	if afterNil || after < before {
		// versionsInRange returns versions in (after, before] ascending order;
		// we reverse to produce highest-first (the order rolled back).
		ascending, err := versionsInRange(req.Source.Dir, after, before, afterNil)
		if err != nil {
			log.Printf("warn: golang-migrate: rolled-back version enumeration failed: %v", err)
		}
		for i := len(ascending) - 1; i >= 0; i-- {
			rolledBack = append(rolledBack, ascending[i])
		}
	}
	// If after >= before, nothing was rolled back — return empty slice.

	return interfaces.MigrationResult{
		Applied:    rolledBack,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Status returns the current migration version and pending migrations.
func (d *Driver) Status(_ context.Context, req interfaces.MigrationRequest) (interfaces.MigrationStatus, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationStatus{}, err
	}
	m, err := newMigrate(req)
	if err != nil {
		return interfaces.MigrationStatus{}, fmt.Errorf("golang-migrate: %w", err)
	}
	defer m.Close() //nolint:errcheck

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return interfaces.MigrationStatus{}, fmt.Errorf("golang-migrate status: %w", err)
	}

	current := ""
	atNil := errors.Is(err, migrate.ErrNilVersion)
	if !atNil {
		current = fmt.Sprintf("%d", version)
	}

	// Enumerate pending migrations (versions in source that exceed current).
	pending, _ := listPendingVersions(req.Source.Dir, version, atNil)

	return interfaces.MigrationStatus{
		Current: current,
		Pending: pending,
		Dirty:   dirty,
	}, nil
}

// Goto migrates to the specified version (up or down).
func (d *Driver) Goto(_ context.Context, req interfaces.MigrationRequest, target string) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	m, err := newMigrate(req)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: %w", err)
	}
	defer m.Close() //nolint:errcheck

	var version uint
	if _, err := fmt.Sscanf(target, "%d", &version); err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate goto: invalid target version %q: %w", target, err)
	}

	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate goto: %w", err)
	}

	return interfaces.MigrationResult{
		Applied:    []string{target},
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// ForceOptions controls safety checks for metadata-only force repair.
type ForceOptions struct {
	// AllowClean permits force-setting a database that is not currently dirty.
	// Leave false for normal repair flows so force is limited to dirty states.
	AllowClean bool
}

// RepairDirtyOptions controls a guarded metadata repair for a known dirty migration.
type RepairDirtyOptions struct {
	ExpectedDirtyVersion string
	ForceVersion         string
	ThenUp               bool
}

// Force sets the recorded migration version without applying migration files.
func (d *Driver) Force(_ context.Context, req interfaces.MigrationRequest, target string, opts ForceOptions) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	version, err := parseForceTarget(target)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	if version > 0 {
		exists, err := versionExists(req.Source.Dir, uint(version))
		if err != nil {
			return interfaces.MigrationResult{}, err
		}
		if !exists {
			return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate force: target version %q does not exist in migration source", target)
		}
	}

	m, err := newMigrate(req)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: %w", err)
	}
	defer m.Close() //nolint:errcheck

	_, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate force: version before force: %w", err)
	}
	if !dirty && !opts.AllowClean {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate force: database is clean; refusing metadata-only force without allow-clean")
	}

	if err := m.Force(version); err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate force: %w", err)
	}

	return interfaces.MigrationResult{
		Applied:    nil,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// RepairDirty verifies the database is dirty at the exact expected version before forcing metadata.
func (d *Driver) RepairDirty(ctx context.Context, req interfaces.MigrationRequest, opts RepairDirtyOptions) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	expected, err := parseExpectedDirtyVersion(opts.ExpectedDirtyVersion)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	forceVersion, err := parseForceTarget(opts.ForceVersion)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	if forceVersion > 0 && uint(forceVersion) > expected {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: force version %q must not be greater than expected dirty version %q", opts.ForceVersion, opts.ExpectedDirtyVersion)
	}

	expectedExists, err := versionExists(req.Source.Dir, expected)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	if !expectedExists {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: expected dirty version %q does not exist in migration source", opts.ExpectedDirtyVersion)
	}
	if forceVersion > 0 {
		forceExists, err := versionExists(req.Source.Dir, uint(forceVersion))
		if err != nil {
			return interfaces.MigrationResult{}, err
		}
		if !forceExists {
			return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: force version %q does not exist in migration source", opts.ForceVersion)
		}
	}

	m, err := newMigrate(req)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate: %w", err)
	}

	current, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		_, _ = m.Close()
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: version before repair: %w", err)
	}
	if !dirty {
		_, _ = m.Close()
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: database is clean; refusing metadata repair")
	}
	if current != expected {
		_, _ = m.Close()
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: database is dirty at version %d, expected %d", current, expected)
	}

	if err := m.Force(forceVersion); err != nil {
		_, _ = m.Close()
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty: force: %w", err)
	}
	_, _ = m.Close()

	if opts.ThenUp {
		result, err := d.Up(ctx, req)
		if err != nil {
			st, statusErr := d.Status(ctx, req)
			if statusErr != nil {
				return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty then-up failed after metadata was repaired to version %s; status after failure unavailable: %v; up error: %w", opts.ForceVersion, statusErr, err)
			}
			return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty then-up failed after metadata was repaired to version %s; current version %s dirty=%t: %w", opts.ForceVersion, st.Current, st.Dirty, err)
		}
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}

	return interfaces.MigrationResult{
		Applied:    nil,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

func parseExpectedDirtyVersion(target string) (uint, error) {
	version, err := strconv.ParseUint(target, 10, 0)
	if err != nil || version == 0 {
		return 0, fmt.Errorf("golang-migrate repair-dirty: invalid expected dirty version %q: must be a positive integer", target)
	}
	return uint(version), nil
}

func parseForceTarget(target string) (int, error) {
	version, err := strconv.Atoi(target)
	if err != nil || version == 0 || version < -1 {
		return 0, fmt.Errorf("golang-migrate force: invalid target version %q: must be -1 or a positive integer", target)
	}
	return version, nil
}

func versionExists(dir string, target uint) (bool, error) {
	src := &migratefile.File{}
	s, err := src.Open("file://" + dir)
	if err != nil {
		return false, fmt.Errorf("golang-migrate: open source for version lookup: %w", err)
	}
	defer s.Close() //nolint:errcheck

	v, err := s.First()
	for {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, fmt.Errorf("golang-migrate: read source version: %w", err)
		}
		if v == target {
			return true, nil
		}
		v, err = s.Next(v)
	}
}

// newMigrate creates a migrate.Migrate from a MigrationRequest.
// The DSN is expected to be a postgres:// URL; we rewrite it to pgx5:// for
// the pgx/v5 driver.
func newMigrate(req interfaces.MigrationRequest) (*migrate.Migrate, error) {
	dsn := req.DSN
	// golang-migrate pgx/v5 driver registers as "pgx5" and expects pgx5:// scheme.
	switch {
	case strings.HasPrefix(dsn, "postgres://"):
		dsn = "pgx5://" + dsn[len("postgres://"):]
	case strings.HasPrefix(dsn, "postgresql://"):
		dsn = "pgx5://" + dsn[len("postgresql://"):]
	}

	sourceURL := "file://" + req.Source.Dir
	return migrate.New(sourceURL, dsn)
}

// versionsInRange opens the file source and returns version strings v where
// lo < v <= hi (exclusive lo, inclusive hi), in ascending order.
// When loNil is true all versions v <= hi qualify (DB had no prior state).
// This replaces the deleted collectApplied() which assumed sequential version
// numbers and would panic on timestamp-based versions (e.g. 20240101000001).
//
// End-of-stream is signalled by os.ErrNotExist per the golang-migrate source.Driver
// contract; any other error from Next() is returned to the caller.
func versionsInRange(dir string, lo, hi uint, loNil bool) ([]string, error) {
	src := &migratefile.File{}
	s, err := src.Open("file://" + dir)
	if err != nil {
		return nil, fmt.Errorf("golang-migrate: open source for version range: %w", err)
	}
	defer s.Close() //nolint:errcheck

	var result []string
	v, err := s.First()
	for {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break // normal end of stream
			}
			return result, fmt.Errorf("golang-migrate: iterate source: %w", err)
		}
		if (loNil || v > lo) && v <= hi {
			result = append(result, fmt.Sprintf("%d", v))
		}
		v, err = s.Next(v)
	}
	return result, nil
}

// listPendingVersions opens the file source and returns the version strings of
// migrations that have not yet been applied (i.e. version > current).
// When atNil is true the DB has no applied migrations, so every version is pending.
func listPendingVersions(dir string, current uint, atNil bool) ([]string, error) {
	src := &migratefile.File{}
	s, err := src.Open("file://" + dir)
	if err != nil {
		return nil, fmt.Errorf("golang-migrate: open source for pending list: %w", err)
	}
	defer s.Close() //nolint:errcheck

	var pending []string
	v, err := s.First()
	for {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				break // normal end of stream
			}
			return pending, fmt.Errorf("golang-migrate: iterate source for pending: %w", err)
		}
		if atNil || v > current {
			pending = append(pending, fmt.Sprintf("%d", v))
		}
		v, err = s.Next(v)
	}
	return pending, nil
}
