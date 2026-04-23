// Package golangmigrate provides a MigrationDriver backed by golang-migrate/migrate/v4.
package golangmigrate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"

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

	after, _, _ := m.Version()
	_ = before
	_ = after

	return interfaces.MigrationResult{
		Applied:    []string{fmt.Sprintf("rolled back %d migration(s)", steps)},
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
	if !errors.Is(err, migrate.ErrNilVersion) {
		current = fmt.Sprintf("%d", version)
	}

	return interfaces.MigrationStatus{
		Current: current,
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
	// golang-migrate pgx/v5 driver expects pgx5:// scheme.
	if len(dsn) >= 10 && dsn[:10] == "postgres://" {
		dsn = "pgx5://" + dsn[len("postgres://"):]
	} else if len(dsn) >= 13 && dsn[:13] == "postgresql://" {
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
