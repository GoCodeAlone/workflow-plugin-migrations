// Package testharness provides a Postgres test harness for migration driver tests.
// It auto-selects an appropriate backend:
//  1. ProvidedDSN — if WORKFLOW_MIGRATE_TEST_DSN env var is set.
//  2. EmbeddedPostgres — Fergus Strange's pure-Go embedded Postgres (default, no Docker required).
package testharness

import (
	"fmt"
	"os"
	"testing"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// Harness wraps a running Postgres instance.
type Harness struct {
	dsn      string
	embedded *embeddedpostgres.EmbeddedPostgres
}

// New creates and starts a new Postgres harness.
// The caller must defer h.Close(t) to stop the server.
func New() (*Harness, error) {
	// 1. Provided DSN.
	if dsn := os.Getenv("WORKFLOW_MIGRATE_TEST_DSN"); dsn != "" {
		return &Harness{dsn: dsn}, nil
	}

	// 2. Embedded Postgres.
	ep := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Username("test").
		Password("test").
		Database("migrations_test").
		Port(15432),
	)
	if err := ep.Start(); err != nil {
		return nil, fmt.Errorf("embedded-postgres start: %w", err)
	}
	dsn := "postgres://test:test@localhost:15432/migrations_test?sslmode=disable"
	return &Harness{dsn: dsn, embedded: ep}, nil
}

// DSN returns the PostgreSQL connection string for this harness.
func (h *Harness) DSN() string { return h.dsn }

// Close stops the embedded Postgres server, if any.
func (h *Harness) Close(t *testing.T) {
	t.Helper()
	if h.embedded != nil {
		if err := h.embedded.Stop(); err != nil {
			t.Logf("embedded-postgres stop: %v", err)
		}
	}
}
