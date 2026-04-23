// Package atlas provides a MigrationDriver backed by ariga.io/atlas v1.
// Atlas uses versioned SQL migration files with an atlas.sum integrity file.
// If atlas.sum is absent the driver auto-generates it so users don't need the
// Atlas CLI installed for basic up/down/status operations.
package atlas

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	atlmigrate "ariga.io/atlas/sql/migrate"
	atlpg "ariga.io/atlas/sql/postgres"

	"github.com/GoCodeAlone/workflow/interfaces"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Driver implements interfaces.MigrationDriver using ariga.io/atlas.
type Driver struct{}

// New returns a new Atlas Driver.
func New() *Driver { return &Driver{} }

// Name returns the driver name used in plugin configuration.
func (d *Driver) Name() string { return "atlas" }

// Up applies all pending migrations.
func (d *Driver) Up(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	db, dir, rrw, drv, cleanup, err := open(req)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	defer cleanup()
	_ = db

	ex, err := atlmigrate.NewExecutor(drv, dir, rrw)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("atlas: executor: %w", err)
	}

	if err := ex.ExecuteN(ctx, 0); err != nil && !errors.Is(err, atlmigrate.ErrNoPendingFiles) {
		return interfaces.MigrationResult{}, fmt.Errorf("atlas up: %w", err)
	}

	// Read applied revisions to report what changed.
	revs, err := rrw.ReadRevisions(ctx)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("atlas up: read revisions: %w", err)
	}

	// Collect versions applied in this run by comparing against what was applied
	// before (we can't diff easily, so report all applied-without-error revisions).
	var applied []string
	for _, r := range revs {
		if r.Error == "" && r.ExecutedAt.After(start.Add(-time.Second)) {
			applied = append(applied, r.Version)
		}
	}

	return interfaces.MigrationResult{
		Applied:    applied,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Down rolls back N migrations (Options.Steps, default 1).
// Atlas versioned migrations do not include rollback SQL natively; this driver
// looks for paired <version>_<desc>.down.sql files alongside the up migration files.
func (d *Driver) Down(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	db, _, rrw, _, cleanup, err := open(req)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	defer cleanup()

	steps := req.Options.Steps
	if steps <= 0 {
		steps = 1
	}

	// Find the last N applied revisions in reverse order.
	revs, err := rrw.ReadRevisions(ctx)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("atlas down: read revisions: %w", err)
	}

	// Filter to successfully applied revisions.
	var applied []*atlmigrate.Revision
	for _, r := range revs {
		if r.Error == "" {
			applied = append(applied, r)
		}
	}
	if len(applied) == 0 {
		return interfaces.MigrationResult{DurationMs: time.Since(start).Milliseconds()}, nil
	}

	// Roll back the last 'steps' migrations in reverse order.
	toRollback := applied
	if len(toRollback) > steps {
		toRollback = toRollback[len(toRollback)-steps:]
	}

	var rolledBack []string
	for i := len(toRollback) - 1; i >= 0; i-- {
		r := toRollback[i]
		if err := runDown(ctx, db, req.Source.Dir, r.Version); err != nil {
			return interfaces.MigrationResult{}, fmt.Errorf("atlas down %s: %w", r.Version, err)
		}
		if err := rrw.DeleteRevision(ctx, r.Version); err != nil {
			return interfaces.MigrationResult{}, fmt.Errorf("atlas down: delete revision %s: %w", r.Version, err)
		}
		rolledBack = append(rolledBack, r.Version)
	}

	return interfaces.MigrationResult{
		Applied:    rolledBack,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// Status returns the current migration state.
func (d *Driver) Status(ctx context.Context, req interfaces.MigrationRequest) (interfaces.MigrationStatus, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationStatus{}, err
	}

	db, dir, rrw, drv, cleanup, err := open(req)
	if err != nil {
		return interfaces.MigrationStatus{}, err
	}
	defer cleanup()
	_ = db
	_ = drv

	ex, err := atlmigrate.NewExecutor(drv, dir, rrw)
	if err != nil {
		return interfaces.MigrationStatus{}, fmt.Errorf("atlas status: executor: %w", err)
	}

	pending, err := ex.Pending(ctx)
	if err != nil && !errors.Is(err, atlmigrate.ErrNoPendingFiles) {
		return interfaces.MigrationStatus{}, fmt.Errorf("atlas status: pending: %w", err)
	}

	var pendingVersions []string
	for _, f := range pending {
		pendingVersions = append(pendingVersions, f.Version())
	}

	// Current = latest applied revision with no error.
	current, err := rrw.currentVersion(ctx)
	if err != nil {
		return interfaces.MigrationStatus{}, fmt.Errorf("atlas status: current version: %w", err)
	}

	// Check for dirty state (last applied revision has an error).
	dirty := false
	revs, _ := rrw.ReadRevisions(ctx)
	if len(revs) > 0 {
		last := revs[len(revs)-1]
		dirty = last.Error != ""
	}

	return interfaces.MigrationStatus{
		Current: current,
		Pending: pendingVersions,
		Dirty:   dirty,
	}, nil
}

// Goto migrates to the specified version (up or down as needed).
func (d *Driver) Goto(ctx context.Context, req interfaces.MigrationRequest, target string) (interfaces.MigrationResult, error) {
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	db, dir, rrw, drv, cleanup, err := open(req)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	defer cleanup()
	_ = db

	current, err := rrw.currentVersion(ctx)
	if err != nil {
		return interfaces.MigrationResult{}, fmt.Errorf("atlas goto: current version: %w", err)
	}

	// Determine direction.
	if target > current {
		// Migrate up to target.
		ex, err := atlmigrate.NewExecutor(drv, dir, rrw)
		if err != nil {
			return interfaces.MigrationResult{}, fmt.Errorf("atlas goto: executor: %w", err)
		}
		if err := ex.ExecuteTo(ctx, target); err != nil && !errors.Is(err, atlmigrate.ErrNoPendingFiles) {
			return interfaces.MigrationResult{}, fmt.Errorf("atlas goto %s: %w", target, err)
		}
	} else if target < current {
		// Roll back until we reach target.
		revs, err := rrw.ReadRevisions(ctx)
		if err != nil {
			return interfaces.MigrationResult{}, fmt.Errorf("atlas goto: read revisions: %w", err)
		}
		for i := len(revs) - 1; i >= 0; i-- {
			r := revs[i]
			if r.Version <= target {
				break
			}
			if err := runDown(ctx, db, req.Source.Dir, r.Version); err != nil {
				return interfaces.MigrationResult{}, fmt.Errorf("atlas goto down %s: %w", r.Version, err)
			}
			if err := rrw.DeleteRevision(ctx, r.Version); err != nil {
				return interfaces.MigrationResult{}, fmt.Errorf("atlas goto: delete revision %s: %w", r.Version, err)
			}
		}
	}

	return interfaces.MigrationResult{
		Applied:    []string{target},
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// open builds all the objects needed to talk to the database and the migration dir.
func open(req interfaces.MigrationRequest) (
	db *sql.DB,
	dir *atlmigrate.LocalDir,
	rrw *sqlRevisionRW,
	drv atlmigrate.Driver,
	cleanup func(),
	err error,
) {
	db, err = sql.Open("pgx", req.DSN)
	if err != nil {
		err = fmt.Errorf("atlas: open db: %w", err)
		return
	}

	dir, err = openDir(req.Source.Dir)
	if err != nil {
		_ = db.Close()
		err = fmt.Errorf("atlas: open dir: %w", err)
		return
	}

	rrw, err = newSQLRevisionRW(db, "")
	if err != nil {
		_ = db.Close()
		err = fmt.Errorf("atlas: revisions table: %w", err)
		return
	}

	drv, err = atlpg.Open(db)
	if err != nil {
		_ = db.Close()
		err = fmt.Errorf("atlas: postgres driver: %w", err)
		return
	}

	cleanup = func() { _ = db.Close() }
	return
}

// openDir opens a LocalDir, auto-generating atlas.sum if it is absent.
func openDir(path string) (*atlmigrate.LocalDir, error) {
	dir, err := atlmigrate.NewLocalDir(path)
	if err != nil {
		return nil, err
	}
	// Auto-generate atlas.sum when missing so the driver works without the Atlas CLI.
	if err := atlmigrate.Validate(dir); errors.Is(err, atlmigrate.ErrChecksumNotFound) {
		files, ferr := dir.Files()
		if ferr != nil {
			return nil, ferr
		}
		sum, serr := atlmigrate.NewHashFile(files)
		if serr != nil {
			return nil, serr
		}
		if werr := atlmigrate.WriteSumFile(dir, sum); werr != nil {
			return nil, werr
		}
	} else if err != nil {
		return nil, err
	}
	return dir, nil
}

// runDown executes the .down.sql file paired with a versioned Atlas migration.
// Files are expected to follow the naming: <version>_<desc>.down.sql.
func runDown(ctx context.Context, db *sql.DB, dir, version string) error {
	// Enumerate .down.sql files matching the version prefix.
	localDir, err := atlmigrate.NewLocalDir(dir)
	if err != nil {
		return err
	}
	files, err := localDir.Files()
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.Version() == version {
			// Look for a paired .down.sql file.
			downName := downFilename(f.Name())
			if downName == "" {
				return fmt.Errorf("atlas: no .down.sql file for version %s", version)
			}
			fh, ferr := localDir.Open(downName)
			if ferr != nil {
				return fmt.Errorf("atlas: open %s: %w", downName, ferr)
			}
			defer fh.Close()
			buf := make([]byte, 1<<20) // 1 MB max
			n, _ := fh.Read(buf)
			downSQL := string(buf[:n])
			if downSQL == "" {
				return fmt.Errorf("atlas: empty .down.sql for version %s", version)
			}
			_, err = db.ExecContext(ctx, downSQL)
			return err
		}
	}
	return fmt.Errorf("atlas: migration version %s not found in directory", version)
}

// downFilename returns the .down.sql counterpart of an Atlas migration filename.
// e.g. "20240101_create_users.sql" → "20240101_create_users.down.sql"
// Returns "" if the name doesn't end in .sql or already has .down.sql.
func downFilename(name string) string {
	const suffix = ".sql"
	const downSuffix = ".down.sql"
	if len(name) <= len(suffix) {
		return ""
	}
	if name[len(name)-len(downSuffix):] == downSuffix {
		return "" // already a down file
	}
	if name[len(name)-len(suffix):] != suffix {
		return ""
	}
	return name[:len(name)-len(suffix)] + downSuffix
}
