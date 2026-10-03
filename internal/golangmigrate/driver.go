// Package golangmigrate provides a MigrationDriver backed by golang-migrate/migrate/v4.
package golangmigrate

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	migratefile "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// Driver implements interfaces.MigrationDriver using golang-migrate.
type Driver struct{}

// New returns a new golang-migrate Driver.
func New() *Driver { return &Driver{} }

// Name returns the driver name.
func (d *Driver) Name() string { return "golang-migrate" }

// Up applies all pending migrations.
func (d *Driver) Up(ctx context.Context, req interfaces.MigrationRequest) (result interfaces.MigrationResult, err error) {
	defer preserveContextError(ctx, &err)
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	m, err := newMigrate(ctx, req)
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
func (d *Driver) Down(ctx context.Context, req interfaces.MigrationRequest) (result interfaces.MigrationResult, err error) {
	defer preserveContextError(ctx, &err)
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	m, err := newMigrate(ctx, req)
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
func (d *Driver) Status(ctx context.Context, req interfaces.MigrationRequest) (status interfaces.MigrationStatus, err error) {
	defer preserveContextError(ctx, &err)
	if err := req.Validate(); err != nil {
		return interfaces.MigrationStatus{}, err
	}
	m, err := newMigrate(ctx, req)
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
func (d *Driver) Goto(ctx context.Context, req interfaces.MigrationRequest, target string) (result interfaces.MigrationResult, err error) {
	defer preserveContextError(ctx, &err)
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()
	m, err := newMigrate(ctx, req)
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
	UpIfClean            bool
}

// Force sets the recorded migration version without applying migration files.
func (d *Driver) Force(ctx context.Context, req interfaces.MigrationRequest, target string, opts ForceOptions) (result interfaces.MigrationResult, err error) {
	defer preserveContextError(ctx, &err)
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	version, err := parseForceTarget(target, "force")
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

	m, err := newMigrate(ctx, req)
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

// RepairDirty verifies a dirty database is at the exact expected version before
// forcing metadata. With UpIfClean, a clean database runs normal up instead.
func (d *Driver) RepairDirty(ctx context.Context, req interfaces.MigrationRequest, opts RepairDirtyOptions) (result interfaces.MigrationResult, err error) {
	defer preserveContextError(ctx, &err)
	if err := req.Validate(); err != nil {
		return interfaces.MigrationResult{}, err
	}
	start := time.Now()

	expected, err := parseExpectedDirtyVersion(opts.ExpectedDirtyVersion)
	if err != nil {
		return interfaces.MigrationResult{}, err
	}
	forceVersion, err := parseForceTarget(opts.ForceVersion, "repair-dirty")
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

	m, err := newMigrate(ctx, req)
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
		if opts.UpIfClean {
			result, err := d.Up(ctx, req)
			if err != nil {
				return interfaces.MigrationResult{}, fmt.Errorf("golang-migrate repair-dirty up-if-clean: %w", err)
			}
			result.DurationMs = time.Since(start).Milliseconds()
			return result, nil
		}
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

	if opts.ThenUp || opts.UpIfClean {
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

func parseForceTarget(target, command string) (int, error) {
	version, err := strconv.Atoi(target)
	if err != nil || version == 0 || version < -1 {
		return 0, fmt.Errorf("golang-migrate %s: invalid target version %q: must be -1 or a positive integer", command, target)
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

// golang-migrate's pgx backend uses background contexts. Bind its SQL connection
// to this operation while retaining the backend's migration and locking logic.
func newMigrate(ctx context.Context, req interfaces.MigrationRequest) (*migrationSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	u, err := url.Parse(req.DSN)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "postgres", "postgresql", "pgx5":
		u.Scheme = "postgres"
	default:
		return nil, fmt.Errorf("golang-migrate: unsupported database scheme %q", u.Scheme)
	}
	backendConfig, err := postgresConfig(u)
	if err != nil {
		return nil, err
	}
	config, err := pgx.ParseConfig(migrate.FilterCustomQuery(u).String())
	if err != nil {
		return nil, err
	}
	config.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: conn, DeadlineDelay: time.Second}
	}
	connector := &contextConnector{Connector: stdlib.GetConnector(*config), ctx: ctx}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	backend, err := pgxmigrate.WithInstance(db, backendConfig)
	if err != nil {
		_ = db.Close()
		// WithInstance can fail after reserving its sql.Conn without releasing it.
		connector.closeConnection()
		return nil, err
	}
	m, err := migrate.NewWithDatabaseInstance("file://"+req.Source.Dir, "pgx5", backend)
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	return &migrationSession{migrationEngine: m, backend: backend}, nil
}

type migrationEngine = migrate.Migrate

type migrationSession struct {
	*migrationEngine
	backend database.Driver
}

// Migrate.Version discards dirty=true for a failed down to the nil version.
// Read the same backend metadata while retaining that fail-closed status.
func (m *migrationSession) Version() (uint, bool, error) {
	version, dirty, err := m.backend.Version()
	if err != nil {
		return 0, false, err
	}
	if version == database.NilVersion {
		return 0, dirty, migrate.ErrNilVersion
	}
	return uint(version), dirty, nil
}

// Preserve the pgx backend's URL options, including the existing advisory-lock
// identity (URL path, schema, table) so old and new clients serialize together.
func postgresConfig(u *url.URL) (*pgxmigrate.Config, error) {
	q := u.Query()
	config := &pgxmigrate.Config{DatabaseName: u.Path, MigrationsTable: q.Get("x-migrations-table"), MultiStatementMaxSize: pgxmigrate.DefaultMultiStatementMaxSize}
	for key, target := range map[string]*bool{"x-migrations-table-quoted": &config.MigrationsTableQuoted, "x-multi-statement": &config.MultiStatementEnabled} {
		if value := q.Get(key); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return nil, fmt.Errorf("golang-migrate: %s: %w", key, err)
			}
			*target = parsed
		}
	}
	if config.MigrationsTableQuoted && config.MigrationsTable != "" && (!strings.HasPrefix(config.MigrationsTable, "\"") || !strings.HasSuffix(config.MigrationsTable, "\"")) {
		return nil, fmt.Errorf("golang-migrate: x-migrations-table must be quoted")
	}
	if value := q.Get("x-statement-timeout"); value != "" {
		ms, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		config.StatementTimeout = time.Duration(ms) * time.Millisecond
	}
	if value := q.Get("x-multi-statement-max-size"); value != "" {
		size, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		if size > 0 {
			config.MultiStatementMaxSize = size
		}
	}
	return config, nil
}

func preserveContextError(ctx context.Context, err *error) {
	if *err != nil && ctx.Err() != nil {
		*err = errors.Join(*err, ctx.Err())
	}
}

type contextConnector struct {
	sqldriver.Connector
	ctx        context.Context
	mu         sync.Mutex
	connection *stdlib.Conn
}

func (c *contextConnector) Connect(context.Context) (sqldriver.Conn, error) {
	conn, err := c.Connector.Connect(c.ctx)
	if err != nil {
		return nil, err
	}
	native := conn.(*stdlib.Conn)
	c.mu.Lock()
	c.connection = native
	c.mu.Unlock()
	return &contextConnection{Conn: native, ctx: c.ctx}, nil
}

func (c *contextConnector) closeConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection != nil {
		_ = c.connection.Close()
	}
}

type contextConnection struct {
	*stdlib.Conn
	ctx context.Context
}

func (c *contextConnection) ExecContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	operation := c.ctx
	// Keep a backend x-statement-timeout if it precedes the caller's deadline.
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		operation, cancel = context.WithDeadline(operation, deadline)
		defer cancel()
	}
	return c.Conn.ExecContext(operation, query, args)
}

func (c *contextConnection) QueryContext(_ context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	return c.Conn.QueryContext(c.ctx, query, args)
}

func (c *contextConnection) Ping(context.Context) error         { return c.Conn.Ping(c.ctx) }
func (c *contextConnection) ResetSession(context.Context) error { return c.Conn.ResetSession(c.ctx) }
func (c *contextConnection) Begin() (sqldriver.Tx, error) {
	return c.BeginTx(c.ctx, sqldriver.TxOptions{})
}
func (c *contextConnection) BeginTx(_ context.Context, opts sqldriver.TxOptions) (sqldriver.Tx, error) {
	return c.Conn.BeginTx(c.ctx, opts)
}
func (c *contextConnection) Prepare(query string) (sqldriver.Stmt, error) {
	return c.PrepareContext(c.ctx, query)
}
func (c *contextConnection) PrepareContext(_ context.Context, query string) (sqldriver.Stmt, error) {
	stmt, err := c.Conn.PrepareContext(c.ctx, query)
	if err != nil {
		return nil, err
	}
	return &contextStatement{Stmt: stmt, ctx: c.ctx}, nil
}

type contextStatement struct {
	sqldriver.Stmt
	ctx context.Context
}

func (s *contextStatement) ExecContext(_ context.Context, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	return s.Stmt.(sqldriver.StmtExecContext).ExecContext(s.ctx, args)
}
func (s *contextStatement) QueryContext(_ context.Context, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	return s.Stmt.(sqldriver.StmtQueryContext).QueryContext(s.ctx, args)
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
