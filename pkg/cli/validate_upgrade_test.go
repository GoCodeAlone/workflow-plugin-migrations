package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/GoCodeAlone/workflow/interfaces"
)

func TestValidateUpgradeRejectsPrePopulatedDatabase(t *testing.T) {
	driver := &fakeMigrationDriver{
		statuses: []interfaces.MigrationStatus{{Current: "2"}},
	}
	_, err := validateUpgrade(context.Background(), driver, fakeMigrationRequest(t), fakeMigrationRequest(t))
	if err == nil {
		t.Fatal("validateUpgrade() error = nil; want pre-populated database rejection")
	}
	if !strings.Contains(err.Error(), "requires an empty database") {
		t.Fatalf("validateUpgrade() error = %v; want empty database rejection", err)
	}
	if driver.upCalls != 0 {
		t.Fatalf("Up() calls = %d; want 0 before preflight rejection", driver.upCalls)
	}
}

func TestValidateUpgradeRejectsInitiallyDirtyDatabase(t *testing.T) {
	driver := &fakeMigrationDriver{
		statuses: []interfaces.MigrationStatus{{Current: "2", Dirty: true}},
	}
	_, err := validateUpgrade(context.Background(), driver, fakeMigrationRequest(t), fakeMigrationRequest(t))
	if err == nil {
		t.Fatal("validateUpgrade() error = nil; want dirty database rejection")
	}
	if !strings.Contains(err.Error(), "initial state is dirty") {
		t.Fatalf("validateUpgrade() error = %v; want dirty database rejection", err)
	}
}

func TestValidateUpgradeRejectsCandidateSourceMissingBaselineVersion(t *testing.T) {
	baselineDir := t.TempDir()
	writeCLISQL(t, baselineDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	writeCLISQL(t, baselineDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")

	candidateDir := t.TempDir()
	writeCLISQL(t, candidateDir, "000002_posts.up.sql", "CREATE TABLE posts (id SERIAL PRIMARY KEY);")
	writeCLISQL(t, candidateDir, "000002_posts.down.sql", "DROP TABLE IF EXISTS posts;")

	driver := &fakeMigrationDriver{
		statuses: []interfaces.MigrationStatus{
			{},
			{Current: "1"},
		},
		upResults: []interfaces.MigrationResult{{Applied: []string{"1"}}},
	}
	_, err := validateUpgrade(context.Background(), driver,
		interfaces.MigrationRequest{DSN: "fake", Source: interfaces.MigrationSource{Dir: baselineDir}},
		interfaces.MigrationRequest{DSN: "fake", Source: interfaces.MigrationSource{Dir: candidateDir}},
	)
	if err == nil {
		t.Fatal("validateUpgrade() error = nil; want candidate source consistency error")
	}
	if !strings.Contains(err.Error(), "does not contain recorded baseline version 1") {
		t.Fatalf("validateUpgrade() error = %v; want missing baseline version", err)
	}
}

func TestValidateUpgradeFailsWhenCandidateStatusStillHasPending(t *testing.T) {
	sourceDir := t.TempDir()
	writeCLISQL(t, sourceDir, "000001_users.up.sql", "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	writeCLISQL(t, sourceDir, "000001_users.down.sql", "DROP TABLE IF EXISTS users;")

	driver := &fakeMigrationDriver{
		statuses: []interfaces.MigrationStatus{
			{},
			{Current: "1"},
			{Current: "1", Pending: []string{"2"}},
		},
		upResults: []interfaces.MigrationResult{
			{Applied: []string{"1"}},
			{},
		},
	}
	_, err := validateUpgrade(context.Background(), driver,
		interfaces.MigrationRequest{DSN: "fake", Source: interfaces.MigrationSource{Dir: sourceDir}},
		interfaces.MigrationRequest{DSN: "fake", Source: interfaces.MigrationSource{Dir: sourceDir}},
	)
	if err == nil {
		t.Fatal("validateUpgrade() error = nil; want candidate pending error")
	}
	if !strings.Contains(err.Error(), "candidate has pending migrations after up") {
		t.Fatalf("validateUpgrade() error = %v; want candidate pending context", err)
	}
}

func fakeMigrationRequest(t *testing.T) interfaces.MigrationRequest {
	t.Helper()
	return interfaces.MigrationRequest{DSN: "fake", Source: interfaces.MigrationSource{Dir: t.TempDir()}}
}

type fakeMigrationDriver struct {
	statuses  []interfaces.MigrationStatus
	upResults []interfaces.MigrationResult
	upCalls   int
}

func (d *fakeMigrationDriver) Name() string { return "fake" }

func (d *fakeMigrationDriver) Up(context.Context, interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	d.upCalls++
	if len(d.upResults) == 0 {
		return interfaces.MigrationResult{}, nil
	}
	result := d.upResults[0]
	d.upResults = d.upResults[1:]
	return result, nil
}

func (d *fakeMigrationDriver) Down(context.Context, interfaces.MigrationRequest) (interfaces.MigrationResult, error) {
	return interfaces.MigrationResult{}, errors.New("not implemented")
}

func (d *fakeMigrationDriver) Status(context.Context, interfaces.MigrationRequest) (interfaces.MigrationStatus, error) {
	if len(d.statuses) == 0 {
		return interfaces.MigrationStatus{}, nil
	}
	status := d.statuses[0]
	d.statuses = d.statuses[1:]
	return status, nil
}

func (d *fakeMigrationDriver) Goto(context.Context, interfaces.MigrationRequest, string) (interfaces.MigrationResult, error) {
	return interfaces.MigrationResult{}, errors.New("not implemented")
}
