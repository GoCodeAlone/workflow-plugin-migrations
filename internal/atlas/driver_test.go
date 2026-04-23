package atlas_test

import (
	"context"
	"os"
	"testing"

	atlmigrate "ariga.io/atlas/sql/migrate"

	atlasdriver "github.com/GoCodeAlone/workflow-plugin-migrations/internal/atlas"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/testharness"
	"github.com/GoCodeAlone/workflow/interfaces"
)

func TestAtlasDriver_Name(t *testing.T) {
	d := atlasdriver.New()
	if got := d.Name(); got != "atlas" {
		t.Errorf("Name() = %q; want %q", got, "atlas")
	}
}

func TestAtlasDriver_UpDownStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires postgres")
	}
	h, err := testharness.New()
	if err != nil {
		t.Skipf("skipping: no postgres available: %v", err)
	}
	defer h.Close(t)

	dir := makeAtlasDir(t)
	ctx := context.Background()
	d := atlasdriver.New()
	req := interfaces.MigrationRequest{
		DSN:    h.DSN(),
		Source: interfaces.MigrationSource{Dir: dir},
	}

	// Status: fresh DB — all pending.
	st, err := d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	if len(st.Pending) == 0 {
		t.Error("expected pending migrations on fresh DB")
	}

	// Up: apply all.
	result, err := d.Up(ctx, req)
	if err != nil {
		t.Fatalf("Up() error: %v", err)
	}
	if len(result.Applied) != 2 {
		t.Errorf("Applied = %v; want 2", result.Applied)
	}

	// Status after up: nothing pending.
	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after up: %v", err)
	}
	if len(st.Pending) > 0 {
		t.Errorf("unexpected pending after up: %v", st.Pending)
	}
	if st.Current == "" {
		t.Error("expected non-empty Current after up")
	}

	// Down: roll back 1.
	req.Options.Steps = 1
	_, err = d.Down(ctx, req)
	if err != nil {
		t.Fatalf("Down() error: %v", err)
	}

	st, err = d.Status(ctx, req)
	if err != nil {
		t.Fatalf("Status() after down: %v", err)
	}
	if len(st.Pending) == 0 {
		t.Error("expected 1 pending migration after rolling back 1")
	}
}

// makeAtlasDir creates a temporary Atlas migration directory with two versioned
// SQL migrations and their down counterparts, plus a valid atlas.sum file.
//
// Down files are placed in a "down/" subdirectory so Atlas does not pick them
// up as additional up migrations (Atlas scans all *.sql files in the top-level
// directory, causing duplicate-version panics when .down.sql files share the
// same version prefix as their corresponding up migration).
func makeAtlasDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	downDir := dir + "/down"
	if err := os.MkdirAll(downDir, 0o755); err != nil {
		t.Fatal(err)
	}

	up1 := []byte("CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL);\n")
	dn1 := []byte("DROP TABLE IF EXISTS users;\n")
	up2 := []byte("CREATE TABLE posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL);\n")
	dn2 := []byte("DROP TABLE IF EXISTS posts;\n")

	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(dir+"/00001_create_users.sql", up1)
	write(dir+"/00002_create_posts.sql", up2)
	write(downDir+"/00001_create_users.down.sql", dn1)
	write(downDir+"/00002_create_posts.down.sql", dn2)

	// Generate atlas.sum so the executor validates successfully.
	// Only the top-level .sql files (up migrations) are included in the sum.
	localDir, err := atlmigrate.NewLocalDir(dir)
	if err != nil {
		t.Fatalf("NewLocalDir: %v", err)
	}
	files, err := localDir.Files()
	if err != nil {
		t.Fatalf("dir.Files(): %v", err)
	}
	sum, err := atlmigrate.NewHashFile(files)
	if err != nil {
		t.Fatalf("NewHashFile: %v", err)
	}
	if err := atlmigrate.WriteSumFile(localDir, sum); err != nil {
		t.Fatalf("WriteSumFile: %v", err)
	}
	return dir
}
