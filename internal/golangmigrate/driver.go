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
	defer m.Close()

	// Capture the current version before applying.
	before, _, _ := m.Version()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate up: %w", err)
	}

	after, _, _ := m.Version()
	applied := collectApplied(before, after)

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
	defer m.Close()

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
	// If we're back at nil version (ErrNilVersion), treat after as 0.
	var rolledBack []string
	if errors.Is(afterErr, migrate.ErrNilVersion) {
		for v := uint(1); v <= before; v++ {
			rolledBack = append(rolledBack, fmt.Sprintf("%d", v))
		}
	} else if after < before {
		for v := after + 1; v <= before; v++ {
			rolledBack = append(rolledBack, fmt.Sprintf("%d", v))
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
	defer m.Close()

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
	defer m.Close()

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

// collectApplied returns a list of version strings between before (exclusive)
// and after (inclusive). If the versions are equal, returns empty.
func collectApplied(before, after uint) []string {
	if after <= before {
		return nil
	}
	applied := make([]string, 0, after-before)
	for v := before + 1; v <= after; v++ {
		applied = append(applied, fmt.Sprintf("%d", v))
	}
	return applied
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
	defer s.Close()

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
