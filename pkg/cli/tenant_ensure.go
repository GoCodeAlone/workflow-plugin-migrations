package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func newTenantEnsureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant-ensure",
		Short: "Ensure a tenant schema exists in the database",
		Long: `Creates the named PostgreSQL schema if it does not already exist.
Idempotent — safe to run on every deployment before running migrations.
Used as a pre-deploy step in multi-tenant environments where each tenant
has its own schema.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dsn, _ := cmd.Flags().GetString("dsn")
			if dsn == "" {
				dsn = os.Getenv("DATABASE_URL")
			}
			if dsn == "" {
				return fmt.Errorf("no DSN: set --dsn or DATABASE_URL env var")
			}
			schema, _ := cmd.Flags().GetString("schema")
			if schema == "" {
				return fmt.Errorf("--schema is required")
			}

			db, err := sql.Open("pgx", dsn)
			if err != nil {
				return fmt.Errorf("tenant-ensure: open db: %w", err)
			}
			defer db.Close()

			if _, err = db.ExecContext(context.Background(),
				fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %q`, schema)); err != nil {
				return fmt.Errorf("tenant-ensure: create schema %q: %w", schema, err)
			}
			fmt.Printf("Schema %q ensured.\n", schema)
			return nil
		},
	}
	cmd.Flags().String("dsn", "", "Database connection string (overrides DATABASE_URL)")
	cmd.Flags().String("schema", "", "Tenant schema name to create if absent (required)")
	_ = cmd.MarkFlagRequired("schema")
	return cmd
}
