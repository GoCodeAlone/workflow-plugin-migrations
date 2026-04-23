// Package goose provides a MigrationDriver backed by pressly/goose/v3.
package goose

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	// pgx stdlib driver for database/sql
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// Driver implements interfaces.MigrationDriver using pressly/goose.
type Driver struct{}

// New returns a new goose Driver.
func New() *Driver { return &Driver{} }

// Name returns the driver name.
func (d *Driver) Name() string { return "goose" }

// Up applies all pending migrations.
func (d *Driver) Up(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	db, err := openDB(req.DSN)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	defer db.Close()

	provider, err := newProvider(db, req)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("goose up: %w", err)
	}

	applied := make([]string, 0, len(results))
	for _, r := range results {
		if r.Error == nil {
			applied = append(applied, fmt.Sprintf("%d", r.Source.Version))
		}
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
	steps := req.Options.Steps
	if steps <= 0 {
		steps = 1
	}

	db, err := openDB(req.DSN)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	defer db.Close()

	provider, err := newProvider(db, req)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}

	var reverted []string
	for i := 0; i < steps; i++ {
		res, err := provider.Down(ctx)
		if err != nil {
			// No more migrations to roll back.
			break
		}
		if res != nil && res.Source != nil {
			reverted = append(reverted, fmt.Sprintf("%d", res.Source.Version))
		}
	}

	return interfaces.MigrationResult{
		Applied:    reverted,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Status returns the current migration state.
func (d *Driver) Status(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationStatus, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationStatus{}, err
	}
	db, err := openDB(req.DSN)
	if err != nil {
		return interfaces.MigrationStatus{}, err
	}
	defer db.Close()

	provider, err := newProvider(db, req)
	if err != nil {
		return interfaces.MigrationStatus{}, err
	}

	migrations, err := provider.Status(ctx)
	if err != nil {
		return interfaces.MigrationStatus{}, fmt.Errorf("goose status: %w", err)
	}

	current := ""
	var pending []string
	for _, ms := range migrations {
		switch ms.State {
		case goose.StateApplied:
			current = fmt.Sprintf("%d", ms.Source.Version)
		case goose.StatePending:
			pending = append(pending, fmt.Sprintf("%d", ms.Source.Version))
		}
	}

	return interfaces.MigrationStatus{
		Current: current,
		Pending: pending,
	}, nil
}

// Goto migrates to the specified version.
func (d *Driver) Goto(ctx context.Context, req interfaces.MigrationRequest, target string) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	var version int64
	if _, err := fmt.Sscanf(target, "%d", &version); err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("goose goto: invalid target version %q: %w", target, err)
	}

	db, err := openDB(req.DSN)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	defer db.Close()

	provider, err := newProvider(db, req)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}

	// Determine direction by checking current status.
	statuses, err := provider.Status(ctx)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("goose goto: status check: %w", err)
	}

	currentVersion := int64(0)
	for _, ms := range statuses {
		if ms.State == goose.StateApplied && ms.Source.Version > currentVersion {
			currentVersion = ms.Source.Version
		}
	}

	var results []*goose.MigrationResult
	if version >= currentVersion {
		results, err = provider.UpTo(ctx, version)
	} else {
		results, err = provider.DownTo(ctx, version)
	}
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("goose goto %s: %w", target, err)
	}

	applied := make([]string, 0, len(results))
	for _, r := range results {
		if r != nil && r.Source != nil && r.Error == nil {
			applied = append(applied, fmt.Sprintf("%d", r.Source.Version))
		}
	}

	return interfaces.MigrationResult{
		Applied:    applied,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

func openDB(dsn string) (*sql.DB, error) {
	// pgx stdlib accepts postgres:// and postgresql:// URLs.
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("goose: open db: %w", err)
	}
	return db, nil
}

func newProvider(db *sql.DB, req interfaces.MigrationRequest) (*goose.Provider, error) {
	migrationsFS := os.DirFS(req.Source.Dir)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrationsFS)
	if err != nil {
		return nil, fmt.Errorf("goose provider: %w", err)
	}
	return provider, nil
}
