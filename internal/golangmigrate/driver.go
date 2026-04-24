// Package golangmigrate provides a MigrationDriver backed by golang-migrate/migrate/v4.
package golangmigrate

import (
	"context"
	"errors"
	"fmt"
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

	// Capture the current version before applying.
	before, _, beforeErr := m.Version()
	beforeNil := errors.Is(beforeErr, migrate.ErrNilVersion)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate up: %w", err)
	}

	after, _, _ := m.Version()

	// Walk the source directory to enumerate applied versions; this is safe
	// for timestamp-based version numbers where after-before can be 10^13.
	applied, _ := versionsInRange(req.Source.Dir, before, after, beforeNil)

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

	before, _, _ := m.Version()

	if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate down: %w", err)
	}

	after, _, afterErr := m.Version()

	// Build list of rolled-back version strings (highest to lowest).
	// Walk the source directory instead of integer-range loops — safe for
	// timestamp-based version numbers where before-after can be 10^13.
	var rolledBack []string
	afterNil := errors.Is(afterErr, migrate.ErrNilVersion)
	if afterNil || after < before {
		// versionsInRange returns versions in (after, before] ascending order;
		// we reverse to produce highest-first (the order rolled back).
		ascending, _ := versionsInRange(req.Source.Dir, after, before, afterNil)
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
func versionsInRange(dir string, lo, hi uint, loNil bool) ([]string, error) {
	src := &migratefile.File{}
	s, err := src.Open("file://" + dir)
	if err != nil {
		return nil, fmt.Errorf("golang-migrate: open source for version range: %w", err)
	}
	defer s.Close() //nolint:errcheck

	var result []string
	v, err := s.First()
	for err == nil {
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
	for err == nil {
		if atNil || v > current {
			pending = append(pending, fmt.Sprintf("%d", v))
		}
		v, err = s.Next(v)
	}
	return pending, nil
}
