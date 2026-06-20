// Package internal implements the workflow-plugin-migrations plugin.
// It provides database.migrations and database.migration_driver module types,
// plus step.migrate_up, step.migrate_down, step.migrate_status, step.migrate_to
// step types. Drivers: golang-migrate, goose.
package internal

import (
	"fmt"

	"github.com/GoCodeAlone/workflow/dynamic"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/steps"
)

// Version is set at build time via -ldflags.
var Version = "0.0.0"

// MigrationsPlugin implements sdk.PluginProvider, sdk.ModuleProvider,
// sdk.StepProvider, sdk.SchemaProvider, and the strict ModuleContracts/
// StepContracts methods consumed by Workflow contract tooling.
type MigrationsPlugin struct{}

// NewPlugin returns a new plugin instance.
func NewPlugin() sdk.PluginProvider {
	return &MigrationsPlugin{}
}

// Manifest returns plugin metadata.
func (p *MigrationsPlugin) Manifest() sdk.PluginManifest {
	return sdk.PluginManifest{
		Name:        "workflow-plugin-migrations",
		Version:     Version,
		Author:      "GoCodeAlone",
		Description: "Database migration plugin: golang-migrate + goose drivers, pre-deploy runner, wfctl db-migrate CLI",
	}
}

// ModuleTypes returns module type names this plugin provides.
func (p *MigrationsPlugin) ModuleTypes() []string {
	return []string{
		"database.migrations",
		"database.migration_driver",
	}
}

// CreateModule creates a module instance of the given type.
func (p *MigrationsPlugin) CreateModule(typeName, name string, cfg map[string]any) (sdk.ModuleInstance, error) {
	switch typeName {
	case "database.migrations":
		return newMigrationsModule(name, cfg)
	case "database.migration_driver":
		return newDriverModule(name, cfg)
	default:
		return nil, fmt.Errorf("workflow-plugin-migrations: unknown module type %q", typeName)
	}
}

// StepTypes returns step type names this plugin provides.
func (p *MigrationsPlugin) StepTypes() []string {
	return []string{
		"step.migrate_up",
		"step.migrate_down",
		"step.migrate_status",
		"step.migrate_to",
	}
}

// CreateStep creates a step instance of the given type.
func (p *MigrationsPlugin) CreateStep(typeName, name string, cfg map[string]any) (sdk.StepInstance, error) {
	switch typeName {
	case "step.migrate_up":
		return steps.NewMigrateUpStep(name, cfg), nil
	case "step.migrate_down":
		return steps.NewMigrateDownStep(name, cfg), nil
	case "step.migrate_status":
		return steps.NewMigrateStatusStep(name, cfg), nil
	case "step.migrate_to":
		return steps.NewMigrateToStep(name, cfg), nil
	default:
		return nil, fmt.Errorf("workflow-plugin-migrations: unknown step type %q", typeName)
	}
}

// ModuleSchemas implements sdk.SchemaProvider. It returns schema metadata for
// each module type provided by this plugin so that the Workflow editor and
// wfctl tooling can provide field completions and documentation.
func (p *MigrationsPlugin) ModuleSchemas() []sdk.ModuleSchemaData {
	return PluginModuleSchemas()
}

// ModuleContracts returns strict field contracts for each module type.
// These contracts are used by the Workflow engine to validate configurations
// at startup and by wfctl audit to check for missing descriptors.
func (p *MigrationsPlugin) ModuleContracts() map[string]*dynamic.FieldContract {
	return moduleContracts()
}

// StepContracts returns strict field contracts for each step type.
// These contracts are used by the Workflow engine to validate step inputs
// at pipeline execution time and by wfctl audit for static analysis.
func (p *MigrationsPlugin) StepContracts() map[string]*dynamic.FieldContract {
	return stepContracts()
}
