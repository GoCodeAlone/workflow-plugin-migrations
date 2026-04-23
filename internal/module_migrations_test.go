package internal

import (
	"context"
	"testing"
)

func TestMigrationsModule_Init_MissingSourceDir(t *testing.T) {
	_, err := newMigrationsModule("test", map[string]any{
		"driver": "golang-migrate",
		// source_dir deliberately omitted
	})
	if err == nil {
		t.Error("expected error for missing source_dir")
	}
}

func TestMigrationsModule_Init_UnknownDriver(t *testing.T) {
	m, err := newMigrationsModule("test", map[string]any{
		"driver":     "unknown-driver",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("newMigrationsModule: %v", err)
	}
	if err := m.Init(); err == nil {
		t.Error("expected error for unknown driver")
	}
}

func TestMigrationsModule_Init_GolangMigrate(t *testing.T) {
	m, err := newMigrationsModule("test", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("newMigrationsModule: %v", err)
	}
	if err := m.Init(); err != nil {
		t.Fatalf("Init() with golang-migrate: %v", err)
	}
}

func TestMigrationsModule_Init_Goose(t *testing.T) {
	m, err := newMigrationsModule("test", map[string]any{
		"driver":     "goose",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("newMigrationsModule: %v", err)
	}
	if err := m.Init(); err != nil {
		t.Fatalf("Init() with goose: %v", err)
	}
}

func TestMigrationsModule_StartStop(t *testing.T) {
	m, err := newMigrationsModule("test", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("newMigrationsModule: %v", err)
	}
	if err := m.Init(); err != nil {
		t.Fatalf("Init(): %v", err)
	}
	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
}
