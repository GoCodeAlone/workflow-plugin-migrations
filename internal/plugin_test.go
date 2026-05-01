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

// TestPlugin_SchemaProvider verifies that the plugin implements sdk.SchemaProvider
// and returns a schema for each advertised module type.
func TestPlugin_SchemaProvider(t *testing.T) {
	p, ok := internal.NewPlugin().(sdk.SchemaProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.SchemaProvider")
	}
	schemas := p.ModuleSchemas()
	if len(schemas) == 0 {
		t.Fatal("ModuleSchemas() returned empty slice")
	}
	want := map[string]bool{
		"database.migrations":       true,
		"database.migration_driver": true,
	}
	for _, s := range schemas {
		delete(want, s.Type)
	}
	if len(want) > 0 {
		t.Errorf("missing module schemas for: %v", want)
	}
}

// TestPlugin_ModuleContracts verifies that each advertised module type has a strict
// field contract descriptor with at least one required input.
func TestPlugin_ModuleContracts(t *testing.T) {
	p := internal.NewPlugin().(*internal.MigrationsPlugin)
	contracts := p.ModuleContracts()
	wantTypes := []string{"database.migrations", "database.migration_driver"}
	for _, typ := range wantTypes {
		c, ok := contracts[typ]
		if !ok {
			t.Errorf("missing module contract for %q", typ)
			continue
		}
		if len(c.RequiredInputs)+len(c.OptionalInputs) == 0 {
			t.Errorf("module contract for %q has no declared inputs", typ)
		}
	}
}

// TestPlugin_StepContracts verifies that each advertised step type has a strict
// field contract descriptor with at least one input and at least one output.
func TestPlugin_StepContracts(t *testing.T) {
	p := internal.NewPlugin().(*internal.MigrationsPlugin)
	contracts := p.StepContracts()
	wantTypes := []string{
		"step.migrate_up",
		"step.migrate_down",
		"step.migrate_status",
		"step.migrate_to",
	}
	for _, typ := range wantTypes {
		c, ok := contracts[typ]
		if !ok {
			t.Errorf("missing step contract for %q", typ)
			continue
		}
		if len(c.RequiredInputs)+len(c.OptionalInputs) == 0 {
			t.Errorf("step contract for %q has no declared inputs", typ)
		}
		if len(c.Outputs) == 0 {
			t.Errorf("step contract for %q has no declared outputs", typ)
		}
	}
}

// TestPlugin_StepSchemas verifies that PluginStepSchemas returns a schema for
// every advertised step type.
func TestPlugin_StepSchemas(t *testing.T) {
	schemas := internal.PluginStepSchemas()
	want := map[string]bool{
		"step.migrate_up":     true,
		"step.migrate_down":   true,
		"step.migrate_status": true,
		"step.migrate_to":     true,
	}
	for _, s := range schemas {
		if s.Type == "" {
			t.Error("step schema has empty Type")
		}
		delete(want, s.Type)
		if len(s.ConfigFields) == 0 {
			t.Errorf("step schema %q has no config fields", s.Type)
		}
		if len(s.Outputs) == 0 {
			t.Errorf("step schema %q has no outputs", s.Type)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing step schemas for: %v", want)
	}
}

// TestPlugin_ContractCoverage ensures contract coverage matches advertised types:
// every module type and step type must have a corresponding strict contract.
func TestPlugin_ContractCoverage(t *testing.T) {
	mp, ok := internal.NewPlugin().(sdk.ModuleProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.ModuleProvider")
	}
	sp, ok := internal.NewPlugin().(sdk.StepProvider)
	if !ok {
		t.Fatal("plugin does not implement sdk.StepProvider")
	}
	p := internal.NewPlugin().(*internal.MigrationsPlugin)
	moduleContracts := p.ModuleContracts()
	stepContracts := p.StepContracts()

	for _, modType := range mp.ModuleTypes() {
		if _, ok := moduleContracts[modType]; !ok {
			t.Errorf("module type %q has no strict contract descriptor", modType)
		}
	}
	for _, stepType := range sp.StepTypes() {
		if _, ok := stepContracts[stepType]; !ok {
			t.Errorf("step type %q has no strict contract descriptor", stepType)
		}
	}
}
