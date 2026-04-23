// Package testharness provides a Postgres test harness for migration driver tests.
// Each call to New() creates an isolated PostgreSQL schema so tests don't
// pollute each other's state.
//
// Backend selection:
//  1. ProvidedDSN — if WORKFLOW_MIGRATE_TEST_DSN env var is set.
//  2. EmbeddedPostgres — Fergus Strange's pure-Go embedded Postgres (default).
package testharness

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Harness wraps a running Postgres instance with an isolated schema.
type Harness struct {
	// dsn is the connection string with search_path set to the isolated schema.
	dsn string
	// schema is the unique schema name created for this harness instance.
	schema string
	// adminConn is used to create and drop the schema.
	adminConn *sql.DB
	// embedded is non-nil when we started the embedded postgres.
	embedded *embeddedpostgres.EmbeddedPostgres
}

// New creates and starts a new Postgres harness with an isolated schema.
// The caller must defer h.Close(t) to clean up the schema and stop the server.
func New() (*Harness, error) {
	baseDSN := os.Getenv("WORKFLOW_MIGRATE_TEST_DSN")

	var ep *embeddedpostgres.EmbeddedPostgres
	if baseDSN == "" {
		// Pick a free port atomically via net.Listen so parallel test processes
		// never collide (math/rand port selection can produce the same value
		// across processes even when auto-seeded).
		port, err := freePort()
		if err != nil {
			return nil, fmt.Errorf("testharness: find free port: %w", err)
		}
		ep = embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
			Username("test").
			Password("test").
			Database("migrations_test").
			Port(uint32(port)),
		)
		if err := ep.Start(); err != nil {
			return nil, fmt.Errorf("embedded-postgres start: %w", err)
		}
		baseDSN = fmt.Sprintf("postgres://test:test@localhost:%d/migrations_test?sslmode=disable", port)
	}

	// Create a unique schema for this harness to isolate from other tests.
	schema := uniqueSchema()
	adminConn, err := sql.Open("pgx", baseDSN)
	if err != nil {
		if ep != nil {
			_ = ep.Stop()
		}
		return nil, fmt.Errorf("testharness: open admin conn: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := adminConn.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		_ = adminConn.Close()
		if ep != nil {
			_ = ep.Stop()
		}
		return nil, fmt.Errorf("testharness: create schema %q: %w", schema, err)
	}

	// Build a DSN that sets search_path so all tables land in our schema.
	schemaDSN := withSearchPath(baseDSN, schema)

	return &Harness{
		dsn:       schemaDSN,
		schema:    schema,
		adminConn: adminConn,
		embedded:  ep,
	}, nil
}

// DSN returns the PostgreSQL connection string for this harness.
// The DSN includes search_path=<schema> for isolation.
func (h *Harness) DSN() string { return h.dsn }

// Close drops the isolated schema and stops the embedded Postgres server if any.
func (h *Harness) Close(t *testing.T) {
	t.Helper()
	if h.adminConn != nil && h.schema != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := h.adminConn.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA %q CASCADE", h.schema)); err != nil {
			t.Logf("testharness: drop schema %q: %v", h.schema, err)
		}
		_ = h.adminConn.Close()
	}
	if h.embedded != nil {
		if err := h.embedded.Stop(); err != nil {
			t.Logf("embedded-postgres stop: %v", err)
		}
	}
}

// freePort asks the OS for an unused TCP port and returns it.
// Using net.Listen avoids port collisions that can occur when multiple test
// processes generate random port numbers concurrently.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// uniqueSchema generates a unique schema name for this test run.
func uniqueSchema() string {
	return fmt.Sprintf("wfm_test_%d_%d", time.Now().UnixNano(), rand.Int63n(9999))
}

// withSearchPath appends search_path=<schema> to a postgres DSN.
// It handles both DSNs with and without existing query parameters.
func withSearchPath(dsn, schema string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&search_path=" + schema
	}
	return dsn + "?search_path=" + schema
}
