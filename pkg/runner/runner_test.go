package runner_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/driver"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/runner"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/golang-migrate/migrate/v4/database"
)

type recordingDriver struct {
	ctx    context.Context
	req    driver.Request
	target string
	op     string
	err    error
	wait   bool
}

func (d *recordingDriver) Name() string { return "recording" }
func (d *recordingDriver) record(ctx context.Context, req driver.Request, op, target string) error {
	d.ctx, d.req, d.op, d.target = ctx, req, op, target
	if d.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return d.err
}
func (d *recordingDriver) Up(ctx context.Context, req driver.Request) (driver.Result, error) {
	return driver.Result{Applied: []string{"1", "2"}}, d.record(ctx, req, "up", "")
}
func (d *recordingDriver) Down(ctx context.Context, req driver.Request) (driver.Result, error) {
	return driver.Result{Applied: []string{"2"}}, d.record(ctx, req, "down", "")
}
func (d *recordingDriver) Goto(ctx context.Context, req driver.Request, target string) (driver.Result, error) {
	return driver.Result{Applied: []string{target}}, d.record(ctx, req, "goto", target)
}
func (d *recordingDriver) Status(ctx context.Context, req driver.Request) (driver.Status, error) {
	return driver.Status{Current: "2", Pending: []string{"3"}, Dirty: true}, d.record(ctx, req, "status", "")
}

func TestPublicRunnerRegistryAndForwarding(t *testing.T) {
	reg := driver.NewDefaultRegistry()
	if got := reg.Names(); !reflect.DeepEqual(got, []string{"atlas", "golang-migrate", "goose"}) {
		t.Fatalf("builtins = %v", got)
	}
	if _, err := runner.New(reg, "unsupported", nil, nil); err == nil {
		t.Fatal("unsupported driver accepted")
	}
	if _, err := runner.New(nil, "recording", nil, nil); err == nil {
		t.Fatal("nil registry accepted")
	}
	if d, err := runner.New(reg, "", nil, nil); err != nil || d.Name() != "golang-migrate" {
		t.Fatalf("default driver = %v, %v", d, err)
	}
	args, env := append([]string(nil), os.Args...), os.Environ()
	for _, op := range []string{"up", "down", "status", "goto"} {
		t.Run(op, func(t *testing.T) {
			native := &recordingDriver{}
			local := driver.NewRegistry()
			local.MustRegister(native)
			var out, diagnostics bytes.Buffer
			r, err := runner.New(local, "recording", &out, &diagnostics)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), contextKey{}, "consumer")
			req := driver.Request{DSN: "postgres://user:private@example.invalid/db", Source: driver.Source{Dir: t.TempDir(), SchemaName: "tenant"}, Options: driver.Options{Steps: 2, Version: "2"}}
			switch op {
			case "up":
				_, err = r.Up(ctx, req)
			case "down":
				_, err = r.Down(ctx, req)
			case "status":
				var st driver.Status
				st, err = r.Status(ctx, req)
				if !st.Dirty || st.Current != "2" {
					t.Fatalf("status = %+v", st)
				}
			case "goto":
				_, err = r.Goto(ctx, req, "2")
				if native.target != "2" {
					t.Fatal("target lost")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if native.op != op || !reflect.DeepEqual(native.req, req) || native.ctx.Value(contextKey{}) != "consumer" {
				t.Fatalf("forwarded %s: %+v", native.op, native.req)
			}
			if out.Len() == 0 || diagnostics.Len() != 0 {
				t.Fatalf("injected IO: out=%q err=%q", out.String(), diagnostics.String())
			}
		})
	}
	if !reflect.DeepEqual(os.Args, args) || !reflect.DeepEqual(os.Environ(), env) {
		t.Fatal("runner mutated process arguments/environment")
	}
}

type contextKey struct{}

func TestRunnerCancellationTimeoutAndValidation(t *testing.T) {
	for _, op := range []string{"up", "down", "status", "goto"} {
		t.Run(op, func(t *testing.T) {
			native := &recordingDriver{}
			reg := driver.NewRegistry()
			reg.MustRegister(native)
			r, _ := runner.New(reg, native.Name(), nil, nil)
			req := driver.Request{DSN: "postgres://local/db", Source: driver.Source{Dir: t.TempDir()}}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			switch op {
			case "up":
				_, err = r.Up(ctx, req)
			case "down":
				_, err = r.Down(ctx, req)
			case "status":
				_, err = r.Status(ctx, req)
			case "goto":
				_, err = r.Goto(ctx, req, "1")
			}
			if !errors.Is(err, context.Canceled) || native.ctx != nil {
				t.Fatalf("pre-cancel = %v; driver called=%t", err, native.ctx != nil)
			}
			if _, err := r.Up(context.Background(), driver.Request{}); !errors.Is(err, interfaces.ErrValidation) {
				t.Fatalf("validation = %v", err)
			}
			native.wait = true
			req.Options.Timeout = 20 * time.Millisecond
			start := time.Now()
			_, err = r.Up(context.Background(), req)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
				t.Fatalf("timeout = %v", err)
			}
			parent, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer stop()
			req.Options.Timeout = time.Hour
			_, err = r.Up(parent, req)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("parent deadline = %v", err)
			}
		})
	}
}

func TestRunnerAtlasInitializationTimeout(t *testing.T) {
	for _, operation := range []string{"up", "down", "status", "goto"} {
		t.Run(operation, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			release, stopped := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(stopped)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				<-release
			}()
			t.Cleanup(func() {
				close(release)
				_ = listener.Close()
				<-stopped
			})
			r, err := runner.New(driver.NewDefaultRegistry(), "atlas", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			req := driver.Request{
				DSN:     "postgres://test:test@" + listener.Addr().String() + "/fixture?sslmode=disable&connect_timeout=3",
				Source:  driver.Source{Dir: t.TempDir()},
				Options: driver.Options{Timeout: 100 * time.Millisecond},
			}
			start := time.Now()
			switch operation {
			case "up":
				_, err = r.Up(context.Background(), req)
			case "down":
				_, err = r.Down(context.Background(), req)
			case "status":
				_, err = r.Status(context.Background(), req)
			case "goto":
				_, err = r.Goto(context.Background(), req, "1")
			}
			if elapsed := time.Since(start); !errors.Is(err, context.DeadlineExceeded) || elapsed > time.Second {
				t.Fatalf("%s stalled initialization: elapsed=%s error=%v", operation, elapsed, err)
			}
		})
	}
}

func TestRunnerErrorsAreBoundedRedactedAndPreserveIdentity(t *testing.T) {
	secret := "runner-p@ss/word"
	dsn := "postgres://runner:" + url.QueryEscape(secret) + "@localhost/db?password=" + url.QueryEscape(secret)
	sentinel := errors.New("driver denied")
	native := &recordingDriver{err: fmt.Errorf("%w: %s %s %s %s", sentinel, dsn, strings.Replace(dsn, "postgres://", "pgx5://", 1), secret, strings.Repeat("x", 12000))}
	reg := driver.NewRegistry()
	reg.MustRegister(native)
	var out, diagnostics bytes.Buffer
	r, _ := runner.New(reg, native.Name(), &out, &diagnostics)
	_, err := r.Up(context.Background(), driver.Request{DSN: dsn, Source: driver.Source{Dir: t.TempDir()}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("lost identity: %v", err)
	}
	for _, text := range []string{err.Error(), diagnostics.String()} {
		if len(text) > 4096 {
			t.Fatalf("unbounded diagnostic: %d bytes", len(text))
		}
		for _, forbidden := range []string{dsn, secret, url.QueryEscape(secret), "pgx5://runner:"} {
			if strings.Contains(text, forbidden) {
				t.Fatal("diagnostic leaked credentials")
			}
		}
	}
	if out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatal("error IO was not isolated")
	}
}

func postgres(t *testing.T) (string, *sql.DB) {
	t.Helper()
	if testing.Short() {
		t.Skip("PostgreSQL integration")
	}
	h, err := testharness.New()
	if err != nil {
		t.Fatalf("PostgreSQL fixture unavailable: %v", err)
	}
	t.Cleanup(func() { h.Close(t) })
	db, err := sql.Open("pgx", h.DSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return h.DSN(), db
}

func migration(t *testing.T, dir, version, up, down string) {
	t.Helper()
	for suffix, body := range map[string]string{"up": up, "down": down} {
		if err := os.WriteFile(filepath.Join(dir, version+"_fixture."+suffix+".sql"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func nativeRunner(t *testing.T) driver.Driver {
	t.Helper()
	r, err := runner.New(driver.NewDefaultRegistry(), "golang-migrate", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPostgresOrderedRepeatTargetDown(t *testing.T) {
	dsn, db := postgres(t)
	dir := t.TempDir()
	versions := []string{"20240101000001", "20240201000001", "20240301000001"}
	for i, v := range versions {
		migration(t, dir, v, fmt.Sprintf("CREATE TABLE ordered_%d(id int);", i), fmt.Sprintf("DROP TABLE ordered_%d;", i))
	}
	r := nativeRunner(t)
	req := driver.Request{DSN: dsn, Source: driver.Source{Dir: dir}}
	st, err := r.Status(context.Background(), req)
	if err != nil || !reflect.DeepEqual(st.Pending, versions) {
		t.Fatalf("initial status = %+v, %v", st, err)
	}
	got, err := r.Up(context.Background(), req)
	if err != nil || !reflect.DeepEqual(got.Applied, versions) {
		t.Fatalf("ordered up = %+v, %v", got, err)
	}
	st, err = r.Status(context.Background(), req)
	if err != nil || st.Dirty || st.Current != versions[2] || len(st.Pending) != 0 {
		t.Fatalf("clean status = %+v, %v", st, err)
	}
	got, err = r.Up(context.Background(), req)
	if err != nil || len(got.Applied) != 0 {
		t.Fatalf("repeat = %+v, %v", got, err)
	}
	if _, err = r.Goto(context.Background(), req, versions[1]); err != nil {
		t.Fatal(err)
	}
	req.Options.Steps = 1
	got, err = r.Down(context.Background(), req)
	if err != nil || !reflect.DeepEqual(got.Applied, []string{versions[1]}) {
		t.Fatalf("down = %+v, %v", got, err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename LIKE 'ordered_%'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("catalog count=%d, %v", count, err)
	}
}

func TestPostgresPartialFailureStaysDirty(t *testing.T) {
	dsn, db := postgres(t)
	dir := t.TempDir()
	migration(t, dir, "1", "CREATE TABLE committed(id int);", "DROP TABLE committed;")
	migration(t, dir, "2", "THIS IS INVALID SQL;", "SELECT 1;")
	migration(t, dir, "3", "CREATE TABLE never_applied(id int);", "DROP TABLE never_applied;")
	r := nativeRunner(t)
	req := driver.Request{DSN: dsn, Source: driver.Source{Dir: dir}}
	if _, err := r.Up(context.Background(), req); err == nil {
		t.Fatal("partial failure accepted")
	}
	st, err := r.Status(context.Background(), req)
	if err != nil || st.Current != "2" || !st.Dirty {
		t.Fatalf("dirty status=%+v, %v", st, err)
	}
	if _, err = r.Up(context.Background(), req); err == nil {
		t.Fatal("dirty state automatically repaired")
	}
	var committed, later bool
	if err = db.QueryRow("SELECT to_regclass('committed') IS NOT NULL, to_regclass('never_applied') IS NOT NULL").Scan(&committed, &later); err != nil || !committed || later {
		t.Fatalf("partial catalog committed=%t later=%t, %v", committed, later, err)
	}
}

func waitQuery(t *testing.T, db *sql.DB, query, application string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var running bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND datname=current_database() AND state='active' AND query=$1 AND application_name=$2)", query, application).Scan(&running); err != nil {
			t.Fatal(err)
		}
		if running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("migration did not reach expected database query")
}

func applicationDSN(t *testing.T, dsn string) (string, string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("task15_%d", time.Now().UnixNano())
	query := u.Query()
	query.Set("application_name", name)
	u.RawQuery = query.Encode()
	return u.String(), name
}

func TestPostgresActiveCancellationStopsWork(t *testing.T) {
	dsn, db := postgres(t)
	dsn, application := applicationDSN(t, dsn)
	dir := t.TempDir()
	query := "SELECT pg_sleep(10); CREATE TABLE late_cancel(id int);"
	migration(t, dir, "1", query, "DROP TABLE IF EXISTS late_cancel;")
	r := nativeRunner(t)
	req := driver.Request{DSN: dsn, Source: driver.Source{Dir: dir}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Up(ctx, req); done <- err }()
	waitQuery(t, db, query, application)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("active cancel=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("cancelled SQL still running")
		if err := <-done; err == nil {
			t.Error("cancelled migration reported success")
		}
	}
	var late, active bool
	if err := db.QueryRow("SELECT to_regclass('late_cancel') IS NOT NULL, EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND state='active' AND query=$1 AND application_name=$2)", query, application).Scan(&late, &active); err != nil || late || active {
		t.Fatalf("cancelled work: late=%t active=%t, %v", late, active, err)
	}
}

func TestPostgresCancelledLockWaitHasNoLateWork(t *testing.T) {
	dsn, db := postgres(t)
	dsn, application := applicationDSN(t, dsn)
	dir := t.TempDir()
	migration(t, dir, "1", "CREATE TABLE after_lock(id int);", "DROP TABLE after_lock;")
	r := nativeRunner(t)
	req := driver.Request{DSN: dsn, Source: driver.Source{Dir: dir}}
	if _, err := r.Status(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var schema string
	if err := db.QueryRow("SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	key, err := database.GenerateAdvisoryLockId(u.Path, schema, "schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	holder, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	if _, err = holder.ExecContext(context.Background(), "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = holder.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := r.Up(ctx, req); done <- e }()
	waitQuery(t, db, "SELECT pg_advisory_lock($1)", application)
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lock cancellation=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("cancelled advisory-lock waiter still running")
		if _, err = holder.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key); err != nil {
			t.Fatal(err)
		}
		<-done
	}
	var late bool
	if err = db.QueryRow("SELECT to_regclass('after_lock') IS NOT NULL").Scan(&late); err != nil || late {
		t.Fatalf("late lock work=%t, %v", late, err)
	}
	if _, err = holder.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Up(context.Background(), req); err != nil {
		t.Fatalf("fresh-context lock recovery=%v", err)
	}
}

func TestPostgresConcurrentUpUsesEngineLock(t *testing.T) {
	dsn, db := postgres(t)
	dir := t.TempDir()
	migration(t, dir, "1", "CREATE TABLE once_only(id int); INSERT INTO once_only VALUES (1); SELECT pg_sleep(0.1);", "DROP TABLE once_only;")
	r := nativeRunner(t)
	req := driver.Request{DSN: dsn, Source: driver.Source{Dir: dir}}
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := r.Up(context.Background(), req); done <- err }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent migration=%v", err)
		}
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM once_only").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate migration work: count=%d, %v", count, err)
	}
}

// Compile a separate main package so acceptance crosses the actual public API
// and process boundary instead of substituting either runner or native driver.
func TestExternalConsumerPostgres(t *testing.T) {
	dsn, _ := postgres(t)
	dir := t.TempDir()
	migration(t, dir, "1", "CREATE TABLE external_applied(id int);", "DROP TABLE external_applied;")
	consumer := t.TempDir()
	source := `package main
import (
 "context"
 "database/sql"
 "errors"
 "fmt"
 "io"
 "os"
 "path/filepath"
 "time"
 "github.com/GoCodeAlone/workflow-plugin-migrations/pkg/driver"
 "github.com/GoCodeAlone/workflow-plugin-migrations/pkg/runner"
 _ "github.com/jackc/pgx/v5/stdlib"
)
func main() {
 r,err:=runner.New(driver.NewDefaultRegistry(),"golang-migrate",io.Discard,io.Discard)
 if err!=nil { panic(err) }
 req:=driver.Request{DSN:os.Args[1],Source:driver.Source{Dir:os.Args[2]}}
 first,err:=r.Up(context.Background(),req);if err!=nil{panic(err)}
 repeat,err:=r.Up(context.Background(),req);if err!=nil{panic(err)}
 st,err:=r.Status(context.Background(),req);if err!=nil{panic(err)}
 if len(first.Applied)!=1||len(repeat.Applied)!=0||st.Current!="1"||st.Dirty{panic("incorrect persisted migration state")}
 for name,sql:=range map[string]string{"2_cancel.up.sql":"SELECT pg_sleep(5); CREATE TABLE external_late(id int);","2_cancel.down.sql":"DROP TABLE IF EXISTS external_late;"}{
  if err=os.WriteFile(filepath.Join(req.Source.Dir,name),[]byte(sql),0600);err!=nil{panic(err)}
 }
 req.Options.Timeout=100*time.Millisecond
 start:=time.Now();_,err=r.Up(context.Background(),req);elapsed:=time.Since(start)
 db,e:=sql.Open("pgx",req.DSN);if e!=nil{panic(e)};defer db.Close()
 var late bool;if e=db.QueryRow("SELECT to_regclass('external_late') IS NOT NULL").Scan(&late);e!=nil{panic(e)}
 cancelled:=errors.Is(err,context.DeadlineExceeded)
 fmt.Printf("applied=%v repeat=%v current=%s cancelled=%t late=%t elapsed=%s\n",first.Applied,repeat.Applied,st.Current,cancelled,late,elapsed)
 if !cancelled||late||elapsed>2*time.Second{os.Exit(1)}
}
`
	path := filepath.Join(consumer, "main.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(consumer, "consumer")
	build := exec.Command("go", "build", "-o", binary, path)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("external consumer build: %v\n%s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	launch := exec.CommandContext(ctx, binary, dsn, dir)
	output, err := launch.CombinedOutput()
	t.Logf("external consumer result: %s", output)
	if err != nil {
		t.Fatalf("external consumer launch: %v", err)
	}
}
