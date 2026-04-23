package atlas

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	atlmigrate "ariga.io/atlas/sql/migrate"
)

const defaultRevisionsTable = "atlas_schema_revisions"

// sqlRevisionRW is a database-backed RevisionReadWriter for Atlas migrations.
// It creates and manages an atlas_schema_revisions table in the target schema.
type sqlRevisionRW struct {
	db    *sql.DB
	table string
}

// newSQLRevisionRW returns a sqlRevisionRW backed by the given *sql.DB.
func newSQLRevisionRW(db *sql.DB, table string) (*sqlRevisionRW, error) {
	if table == "" {
		table = defaultRevisionsTable
	}
	rw := &sqlRevisionRW{db: db, table: table}
	if err := rw.init(context.Background()); err != nil {
		return nil, err
	}
	return rw, nil
}

// Ident satisfies RevisionReadWriter.
func (r *sqlRevisionRW) Ident() *atlmigrate.TableIdent {
	return &atlmigrate.TableIdent{Name: r.table}
}

// init creates the revisions table if it does not exist.
func (r *sqlRevisionRW) init(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %q (
			version         TEXT        NOT NULL PRIMARY KEY,
			description     TEXT        NOT NULL DEFAULT '',
			type            INTEGER     NOT NULL DEFAULT 0,
			applied         INTEGER     NOT NULL DEFAULT 0,
			total           INTEGER     NOT NULL DEFAULT 0,
			executed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			execution_time  BIGINT      NOT NULL DEFAULT 0,
			error           TEXT,
			error_stmt      TEXT,
			hash            TEXT        NOT NULL DEFAULT '',
			operator_version TEXT       NOT NULL DEFAULT ''
		)`, r.table))
	return err
}

// ReadRevisions returns all applied revisions ordered by version.
func (r *sqlRevisionRW) ReadRevisions(ctx context.Context) ([]*atlmigrate.Revision, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT version, description, type, applied, total, executed_at, execution_time,
		        COALESCE(error,''), COALESCE(error_stmt,''), hash, operator_version
		 FROM %q ORDER BY version`, r.table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRevisions(rows)
}

// ReadRevision returns the revision for a specific version.
func (r *sqlRevisionRW) ReadRevision(ctx context.Context, v string) (*atlmigrate.Revision, error) {
	row := r.db.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT version, description, type, applied, total, executed_at, execution_time,
		        COALESCE(error,''), COALESCE(error_stmt,''), hash, operator_version
		 FROM %q WHERE version = $1`, r.table), v)
	rev, err := scanRevision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, atlmigrate.ErrRevisionNotExist
	}
	return rev, err
}

// WriteRevision upserts a revision record.
func (r *sqlRevisionRW) WriteRevision(ctx context.Context, rev *atlmigrate.Revision) error {
	_, err := r.db.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %q (version, description, type, applied, total, executed_at, execution_time,
		               error, error_stmt, hash, operator_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		ON CONFLICT (version) DO UPDATE SET
			description     = EXCLUDED.description,
			type            = EXCLUDED.type,
			applied         = EXCLUDED.applied,
			total           = EXCLUDED.total,
			executed_at     = EXCLUDED.executed_at,
			execution_time  = EXCLUDED.execution_time,
			error           = EXCLUDED.error,
			error_stmt      = EXCLUDED.error_stmt,
			hash            = EXCLUDED.hash,
			operator_version = EXCLUDED.operator_version`, r.table),
		rev.Version, rev.Description, int(rev.Type), rev.Applied, rev.Total,
		rev.ExecutedAt, int64(rev.ExecutionTime),
		rev.Error, rev.ErrorStmt, rev.Hash, rev.OperatorVersion,
	)
	return err
}

// DeleteRevision removes a revision record by version.
func (r *sqlRevisionRW) DeleteRevision(ctx context.Context, v string) error {
	_, err := r.db.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %q WHERE version = $1`, r.table), v)
	return err
}

// currentVersion returns the latest applied version, or "" if none.
func (r *sqlRevisionRW) currentVersion(ctx context.Context) (string, error) {
	var v string
	err := r.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT version FROM %q WHERE error = '' ORDER BY version DESC LIMIT 1`, r.table)).
		Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func scanRevisions(rows *sql.Rows) ([]*atlmigrate.Revision, error) {
	var revs []*atlmigrate.Revision
	for rows.Next() {
		rev := &atlmigrate.Revision{}
		var execNs int64
		var executedAt time.Time
		var revType int
		if err := rows.Scan(
			&rev.Version, &rev.Description, &revType, &rev.Applied, &rev.Total,
			&executedAt, &execNs, &rev.Error, &rev.ErrorStmt, &rev.Hash, &rev.OperatorVersion,
		); err != nil {
			return nil, err
		}
		rev.Type = atlmigrate.RevisionType(revType)
		rev.ExecutedAt = executedAt
		rev.ExecutionTime = time.Duration(execNs)
		revs = append(revs, rev)
	}
	return revs, rows.Err()
}

func scanRevision(row *sql.Row) (*atlmigrate.Revision, error) {
	rev := &atlmigrate.Revision{}
	var execNs int64
	var executedAt time.Time
	var revType int
	err := row.Scan(
		&rev.Version, &rev.Description, &revType, &rev.Applied, &rev.Total,
		&executedAt, &execNs, &rev.Error, &rev.ErrorStmt, &rev.Hash, &rev.OperatorVersion,
	)
	if err != nil {
		return nil, err
	}
	rev.Type = atlmigrate.RevisionType(revType)
	rev.ExecutedAt = executedAt
	rev.ExecutionTime = time.Duration(execNs)
	return rev, nil
}
