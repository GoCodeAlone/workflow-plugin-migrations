package steps_test

import (
	"context"
	"testing"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/steps"
)

func TestNewMigrateUpStep_Creation(t *testing.T) {
	s := steps.NewMigrateUpStep("my-up", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
		"dsn":        "postgres://localhost/test",
	})
	if s == nil {
		t.Error("NewMigrateUpStep returned nil")
	}
}

func TestNewMigrateDownStep_Creation(t *testing.T) {
	s := steps.NewMigrateDownStep("my-down", map[string]any{
		"driver":     "goose",
		"source_dir": "/tmp/migrations",
		"steps":      2,
	})
	if s == nil {
		t.Error("NewMigrateDownStep returned nil")
	}
}

func TestNewMigrateStatusStep_Creation(t *testing.T) {
	s := steps.NewMigrateStatusStep("my-status", map[string]any{
		"source_dir": "/tmp/migrations",
	})
	if s == nil {
		t.Error("NewMigrateStatusStep returned nil")
	}
}

func TestNewMigrateToStep_Creation(t *testing.T) {
	s := steps.NewMigrateToStep("my-to", map[string]any{
		"source_dir": "/tmp/migrations",
		"target":     "5",
	})
	if s == nil {
		t.Error("NewMigrateToStep returned nil")
	}
}

func TestMigrateToStep_MissingTarget(t *testing.T) {
	s := steps.NewMigrateToStep("my-to", map[string]any{
		"source_dir": "/tmp/migrations",
		// target deliberately omitted
	})
	// Execute without target should fail.
	_, err := s.Execute(context.Background(), nil, nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error when target is missing")
	}
}

func TestMigrateUpStep_MissingSourceDir(t *testing.T) {
	s := steps.NewMigrateUpStep("my-up", map[string]any{
		"dsn": "postgres://localhost/test",
		// source_dir omitted
	})
	_, err := s.Execute(context.Background(), nil, nil, nil, nil, nil)
	if err == nil {
		t.Error("expected error when source_dir is missing")
	}
}
