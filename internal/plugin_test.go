package internal_test

import (
	"testing"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal"
)

func TestPlugin_Manifest(t *testing.T) {
	p := internal.NewPlugin()
	m := p.Manifest()
	if m.Name == "" {
		t.Error("Manifest.Name is empty")
	}
	if m.Version == "" {
		t.Error("Manifest.Version is empty")
	}
}

func TestPlugin_ModuleTypes(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	types := p.ModuleTypes()
	want := map[string]bool{
		"database.migrations":       true,
		"database.migration_driver": true,
	}
	for _, tp := range types {
		delete(want, tp)
	}
	if len(want) > 0 {
		t.Errorf("missing module types: %v", want)
	}
}

func TestPlugin_StepTypes(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	types := p.StepTypes()
	want := map[string]bool{
		"step.migrate_up":     true,
		"step.migrate_down":   true,
		"step.migrate_status": true,
		"step.migrate_to":     true,
	}
	for _, tp := range types {
		delete(want, tp)
	}
	if len(want) > 0 {
		t.Errorf("missing step types: %v", want)
	}
}

func TestPlugin_CreateModule_Unknown(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	_, err := p.CreateModule("unknown.type", "x", nil)
	if err == nil {
		t.Error("expected error for unknown module type")
	}
}

func TestPlugin_CreateStep_Unknown(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	_, err := p.CreateStep("step.unknown", "x", nil)
	if err == nil {
		t.Error("expected error for unknown step type")
	}
}

func TestPlugin_CreateStep_MigrateUp(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	step, err := p.CreateStep("step.migrate_up", "my-step", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("CreateStep(step.migrate_up): %v", err)
	}
	if step == nil {
		t.Error("CreateStep returned nil")
	}
}

func TestPlugin_CreateModule_MigrationsValidConfig(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	m, err := p.CreateModule("database.migrations", "my-db", map[string]any{
		"driver":     "golang-migrate",
		"source_dir": "/tmp/migrations",
	})
	if err != nil {
		t.Fatalf("CreateModule(database.migrations): %v", err)
	}
	if err := m.Init(); err != nil {
		t.Fatalf("Init(): %v", err)
	}
}
