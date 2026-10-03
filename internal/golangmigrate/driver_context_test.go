package golangmigrate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
	"github.com/GoCodeAlone/workflow/interfaces"
)

func contextFixture(t *testing.T) (interfaces.MigrationRequest, *sql.DB) {
	t.Helper()
	if testing.Short() {
		t.Skip("PostgreSQL integration")
	}
	h, err := testharness.New()
	if err != nil {
		t.Fatalf("PostgreSQL unavailable: %v", err)
	}
	t.Cleanup(func() { h.Close(t) })
	db, err := sql.Open("pgx", h.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	writeSQL(t, dir, "1_context.up.sql", "CREATE TABLE context_work(id int);")
	writeSQL(t, dir, "1_context.down.sql", "DROP TABLE context_work;")
	return interfaces.MigrationRequest{DSN: h.DSN(), Source: interfaces.MigrationSource{Dir: dir}}, db
}

func TestDriverContextPreCancelledMethodsDoNotConnect(t *testing.T) {
	for _, operation := range []string{"up", "down", "status", "goto", "force", "repair-dirty"} {
		t.Run(operation, func(t *testing.T) {
			req, db := contextFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			d := golangmigrate.New()
			var err error
			switch operation {
			case "up":
				_, err = d.Up(ctx, req)
			case "down":
				_, err = d.Down(ctx, req)
			case "status":
				_, err = d.Status(ctx, req)
			case "goto":
				_, err = d.Goto(ctx, req, "1")
			case "force":
				_, err = d.Force(ctx, req, "1", golangmigrate.ForceOptions{AllowClean: true})
			case "repair-dirty":
				_, err = d.RepairDirty(ctx, req, golangmigrate.RepairDirtyOptions{ExpectedDirtyVersion: "1", ForceVersion: "1", UpIfClean: true})
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s error=%v; want context.Canceled", operation, err)
			}
			var mutated bool
			if err = db.QueryRow("SELECT to_regclass('schema_migrations') IS NOT NULL OR to_regclass('context_work') IS NOT NULL").Scan(&mutated); err != nil || mutated {
				t.Fatalf("pre-cancel database mutation=%t, %v", mutated, err)
			}
		})
	}
}

func TestDriverContextActiveDownAndGotoStopSQL(t *testing.T) {
	for _, operation := range []string{"down", "goto"} {
		t.Run(operation, func(t *testing.T) {
			req, db := contextFixture(t)
			u, err := url.Parse(req.DSN)
			if err != nil {
				t.Fatal(err)
			}
			application := fmt.Sprintf("native_task15_%d", time.Now().UnixNano())
			q := u.Query()
			q.Set("application_name", application)
			u.RawQuery = q.Encode()
			req.DSN = u.String()
			d := golangmigrate.New()
			if operation == "down" {
				if _, err = d.Up(context.Background(), req); err != nil {
					t.Fatal(err)
				}
			}
			query := "SELECT pg_sleep(10); CREATE TABLE native_late(id int);"
			suffix := "up"
			if operation == "down" {
				suffix = "down"
			}
			writeSQL(t, req.Source.Dir, "1_context."+suffix+".sql", query)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var e error
				if operation == "down" {
					_, e = d.Down(ctx, req)
				} else {
					_, e = d.Goto(ctx, req, "1")
				}
				done <- e
			}()
			waitCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			for {
				var active bool
				if err = db.QueryRowContext(waitCtx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND query=$2 AND state='active')", application, query).Scan(&active); err != nil {
					t.Fatal(err)
				}
				if active {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancel()
			select {
			case err = <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("native active cancellation=%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Error("native cancelled SQL still running")
				<-done
			}
			var late, active bool
			if err = db.QueryRow("SELECT to_regclass('native_late') IS NOT NULL, EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND state='active')", application).Scan(&late, &active); err != nil || late || active {
				t.Fatalf("native late=%t active=%t, %v", late, active, err)
			}
			st, e := d.Status(context.Background(), req)
			current := "1"
			if operation == "down" {
				current = ""
			}
			if e != nil || !st.Dirty || st.Current != current {
				t.Fatalf("cancelled version not dirty: %+v, %v", st, e)
			}
		})
	}
}

func TestDriverContextPreservesNativeOptions(t *testing.T) {
	req, db := contextFixture(t)
	u, err := url.Parse(req.DSN)
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	if err = db.QueryRow("SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("x-migrations-table", fmt.Sprintf("%q.%q", schema, "context_versions"))
	q.Set("x-migrations-table-quoted", "true")
	q.Set("x-multi-statement", "true")
	q.Set("x-multi-statement-max-size", "2048")
	q.Set("x-statement-timeout", "3000")
	u.RawQuery = q.Encode()
	req.DSN = u.String()
	writeSQL(t, req.Source.Dir, "1_context.up.sql", "CREATE TABLE context_work(id int); INSERT INTO context_work VALUES (1);")
	d := golangmigrate.New()
	result, err := d.Up(context.Background(), req)
	if err != nil || len(result.Applied) != 1 {
		t.Fatalf("native options up=%+v, %v", result, err)
	}
	st, err := d.Status(context.Background(), req)
	if err != nil || st.Current != "1" || st.Dirty {
		t.Fatalf("custom version table status=%+v, %v", st, err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM context_work").Scan(&count); err != nil || count != 1 {
		t.Fatalf("multi-statement count=%d, %v", count, err)
	}
}
