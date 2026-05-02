package internal

import (
	"github.com/GoCodeAlone/workflow/dynamic"
	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
	"github.com/GoCodeAlone/workflow/schema"
)

// moduleContracts returns field contracts for each module type provided by this plugin.
// A contract describes the required/optional config inputs and outputs for a module type
// so that the Workflow engine and audit tooling can validate configurations statically.
func moduleContracts() map[string]*dynamic.FieldContract {
	return map[string]*dynamic.FieldContract{
		"database.migrations":       migrationsModuleContract(),
		"database.migration_driver": migrationDriverModuleContract(),
	}
}

// stepContracts returns field contracts for each step type provided by this plugin.
func stepContracts() map[string]*dynamic.FieldContract {
	return map[string]*dynamic.FieldContract{
		"step.migrate_up":     migrateUpContract(),
		"step.migrate_down":   migrateDownContract(),
		"step.migrate_status": migrateStatusContract(),
		"step.migrate_to":     migrateToContract(),
	}
}

// migrationsModuleContract returns the field contract for the database.migrations module.
func migrationsModuleContract() *dynamic.FieldContract {
	c := dynamic.NewFieldContract()
	c.RequiredInputs["source_dir"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Directory containing migration files",
	}
	c.OptionalInputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Migration driver: golang-migrate (default) or goose",
		Default:     "golang-migrate",
	}
	c.OptionalInputs["driver_ref"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Literal driver name fallback (module-ref lookup is not yet implemented; treated as a driver name string)",
	}
	c.OptionalInputs["dsn_env"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Environment variable name holding the database DSN",
		Default:     "DATABASE_URL",
	}
	c.OptionalInputs["history_table"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Migration history table name (reserved for future driver support; currently has no effect at runtime)",
	}
	c.OptionalInputs["timeout"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Operation timeout as a Go duration string (e.g. 5m)",
		Default:     "5m",
	}
	return c
}

// migrationDriverModuleContract returns the field contract for the database.migration_driver module.
func migrationDriverModuleContract() *dynamic.FieldContract {
	c := dynamic.NewFieldContract()
	c.RequiredInputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Driver name: golang-migrate, goose, or atlas",
	}
	c.Outputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Resolved driver name (returned by the driver_name method)",
	}
	return c
}

// migrateUpContract returns the field contract for step.migrate_up.
func migrateUpContract() *dynamic.FieldContract {
	c := dynamic.NewFieldContract()
	c.RequiredInputs["source_dir"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Directory containing migration files",
	}
	c.OptionalInputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Migration driver: golang-migrate (default) or goose",
		Default:     "golang-migrate",
	}
	c.OptionalInputs["dsn_env"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Environment variable name holding the database DSN",
		Default:     "DATABASE_URL",
	}
	c.OptionalInputs["dsn"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Literal database DSN (overrides dsn_env)",
	}
	c.OptionalInputs["timeout"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Operation timeout as a Go duration string (e.g. 5m)",
		Default:     "5m",
	}
	c.Outputs["applied"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations applied",
	}
	c.Outputs["skipped"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations skipped (already applied)",
	}
	c.Outputs["duration_ms"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Total execution time in milliseconds",
	}
	return c
}

// migrateDownContract returns the field contract for step.migrate_down.
func migrateDownContract() *dynamic.FieldContract {
	c := dynamic.NewFieldContract()
	c.RequiredInputs["source_dir"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Directory containing migration files",
	}
	c.OptionalInputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Migration driver: golang-migrate (default) or goose",
		Default:     "golang-migrate",
	}
	c.OptionalInputs["dsn_env"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Environment variable name holding the database DSN",
		Default:     "DATABASE_URL",
	}
	c.OptionalInputs["dsn"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Literal database DSN (overrides dsn_env)",
	}
	c.OptionalInputs["timeout"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Operation timeout as a Go duration string (e.g. 5m)",
		Default:     "5m",
	}
	c.OptionalInputs["steps"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations to roll back",
		Default:     1,
	}
	c.Outputs["applied"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations rolled back",
	}
	c.Outputs["skipped"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations skipped",
	}
	c.Outputs["duration_ms"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Total execution time in milliseconds",
	}
	return c
}

// migrateStatusContract returns the field contract for step.migrate_status.
func migrateStatusContract() *dynamic.FieldContract {
	c := dynamic.NewFieldContract()
	c.RequiredInputs["source_dir"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Directory containing migration files",
	}
	c.OptionalInputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Migration driver: golang-migrate (default) or goose",
		Default:     "golang-migrate",
	}
	c.OptionalInputs["dsn_env"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Environment variable name holding the database DSN",
		Default:     "DATABASE_URL",
	}
	c.OptionalInputs["dsn"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Literal database DSN (overrides dsn_env)",
	}
	c.OptionalInputs["timeout"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Operation timeout as a Go duration string (e.g. 5m)",
		Default:     "5m",
	}
	c.Outputs["current"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Current applied migration version",
	}
	c.Outputs["pending"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of pending (unapplied) migrations",
	}
	c.Outputs["dirty"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeBool,
		Description: "Whether the database is in a dirty migration state",
	}
	return c
}

// migrateToContract returns the field contract for step.migrate_to.
func migrateToContract() *dynamic.FieldContract {
	c := dynamic.NewFieldContract()
	c.RequiredInputs["source_dir"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Directory containing migration files",
	}
	c.RequiredInputs["target"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Target migration version to migrate to",
	}
	c.OptionalInputs["driver"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Migration driver: golang-migrate (default) or goose",
		Default:     "golang-migrate",
	}
	c.OptionalInputs["dsn_env"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Environment variable name holding the database DSN",
		Default:     "DATABASE_URL",
	}
	c.OptionalInputs["dsn"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Literal database DSN (overrides dsn_env)",
	}
	c.OptionalInputs["timeout"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeString,
		Description: "Operation timeout as a Go duration string (e.g. 5m)",
		Default:     "5m",
	}
	c.Outputs["applied"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations applied or rolled back to reach the target",
	}
	c.Outputs["skipped"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Number of migrations skipped",
	}
	c.Outputs["duration_ms"] = dynamic.FieldSpec{
		Type:        dynamic.FieldTypeInt,
		Description: "Total execution time in milliseconds",
	}
	return c
}

// PluginModuleSchemas returns the module schema data for all module types
// provided by this plugin. This is consumed by the Workflow SDK SchemaProvider
// interface and by wfctl editor schema generation.
func PluginModuleSchemas() []sdk.ModuleSchemaData {
	return []sdk.ModuleSchemaData{
		{
			Type:        "database.migrations",
			Label:       "Database Migrations",
			Category:    "database",
			Description: "Manages database migrations using golang-migrate or goose drivers. Provides up/down/status/goto operations.",
			ConfigFields: []sdk.ConfigField{
				{Name: "source_dir", Type: "string", Description: "Directory containing migration files", Required: true},
				{Name: "driver", Type: "string", Description: "Migration driver: golang-migrate (default) or goose", DefaultValue: "golang-migrate"},
				{Name: "driver_ref", Type: "string", Description: "Reference to a database.migration_driver module (or literal driver name)"},
				{Name: "dsn_env", Type: "string", Description: "Environment variable name holding the database DSN", DefaultValue: "DATABASE_URL"},
				{Name: "history_table", Type: "string", Description: "Migration history table name (driver default if empty)"},
				{Name: "timeout", Type: "string", Description: "Operation timeout as a Go duration string (e.g. 5m)", DefaultValue: "5m"},
			},
			Outputs: []sdk.ServiceIO{
				{Name: "applied", Type: "int", Description: "Number of migrations applied (up/down/goto)"},
				{Name: "skipped", Type: "int", Description: "Number of migrations skipped"},
				{Name: "duration_ms", Type: "int", Description: "Operation time in milliseconds"},
				{Name: "current", Type: "string", Description: "Current migration version (status method)"},
				{Name: "pending", Type: "int", Description: "Pending migration count (status method)"},
				{Name: "dirty", Type: "bool", Description: "Dirty migration state flag (status method)"},
			},
		},
		{
			Type:        "database.migration_driver",
			Label:       "Migration Driver",
			Category:    "database",
			Description: "Declares a named migration driver (golang-migrate, goose, or atlas) for reference by database.migrations modules.",
			ConfigFields: []sdk.ConfigField{
				{Name: "driver", Type: "string", Description: "Driver name: golang-migrate, goose, or atlas", Required: true, Options: []string{"golang-migrate", "goose", "atlas"}},
			},
			Outputs: []sdk.ServiceIO{
				{Name: "driver", Type: "string", Description: "The resolved driver name (returned by the driver_name method)"},
			},
		},
	}
}

// PluginStepSchemas returns the step schema data for all step types
// provided by this plugin. These are serialized into plugin.json as stepSchemas
// and consumed by the wfctl LSP / editor schema registry.
func PluginStepSchemas() []*schema.StepSchema {
	return []*schema.StepSchema{
		{
			Type:        "step.migrate_up",
			Plugin:      "workflow-plugin-migrations",
			Description: "Applies all pending database migrations (up direction).",
			ConfigFields: []schema.ConfigFieldDef{
				{Key: "source_dir", Label: "Source Directory", Type: schema.FieldTypeString, Description: "Directory containing migration files", Required: true},
				{Key: "driver", Label: "Driver", Type: schema.FieldTypeSelect, Description: "Migration driver to use", Options: []string{"golang-migrate", "goose"}, DefaultValue: "golang-migrate"},
				{Key: "dsn_env", Label: "DSN Env Var", Type: schema.FieldTypeString, Description: "Environment variable name holding the database DSN", DefaultValue: "DATABASE_URL"},
				{Key: "dsn", Label: "DSN", Type: schema.FieldTypeString, Description: "Literal database DSN (overrides dsn_env)", Sensitive: true},
				{Key: "timeout", Label: "Timeout", Type: schema.FieldTypeDuration, Description: "Operation timeout (e.g. 5m)", DefaultValue: "5m"},
			},
			Outputs: []schema.StepOutputDef{
				{Key: "applied", Type: "int", Description: "Number of migrations applied"},
				{Key: "skipped", Type: "int", Description: "Number of migrations skipped (already applied)"},
				{Key: "duration_ms", Type: "int", Description: "Total execution time in milliseconds"},
			},
		},
		{
			Type:        "step.migrate_down",
			Plugin:      "workflow-plugin-migrations",
			Description: "Rolls back N database migrations (down direction).",
			ConfigFields: []schema.ConfigFieldDef{
				{Key: "source_dir", Label: "Source Directory", Type: schema.FieldTypeString, Description: "Directory containing migration files", Required: true},
				{Key: "driver", Label: "Driver", Type: schema.FieldTypeSelect, Description: "Migration driver to use", Options: []string{"golang-migrate", "goose"}, DefaultValue: "golang-migrate"},
				{Key: "dsn_env", Label: "DSN Env Var", Type: schema.FieldTypeString, Description: "Environment variable name holding the database DSN", DefaultValue: "DATABASE_URL"},
				{Key: "dsn", Label: "DSN", Type: schema.FieldTypeString, Description: "Literal database DSN (overrides dsn_env)", Sensitive: true},
				{Key: "timeout", Label: "Timeout", Type: schema.FieldTypeDuration, Description: "Operation timeout (e.g. 5m)", DefaultValue: "5m"},
				{Key: "steps", Label: "Steps", Type: schema.FieldTypeNumber, Description: "Number of migrations to roll back", DefaultValue: 1},
			},
			Outputs: []schema.StepOutputDef{
				{Key: "applied", Type: "int", Description: "Number of migrations rolled back"},
				{Key: "skipped", Type: "int", Description: "Number of migrations skipped"},
				{Key: "duration_ms", Type: "int", Description: "Total execution time in milliseconds"},
			},
		},
		{
			Type:        "step.migrate_status",
			Plugin:      "workflow-plugin-migrations",
			Description: "Returns the current database migration status (current version, pending count, dirty flag).",
			ConfigFields: []schema.ConfigFieldDef{
				{Key: "source_dir", Label: "Source Directory", Type: schema.FieldTypeString, Description: "Directory containing migration files", Required: true},
				{Key: "driver", Label: "Driver", Type: schema.FieldTypeSelect, Description: "Migration driver to use", Options: []string{"golang-migrate", "goose"}, DefaultValue: "golang-migrate"},
				{Key: "dsn_env", Label: "DSN Env Var", Type: schema.FieldTypeString, Description: "Environment variable name holding the database DSN", DefaultValue: "DATABASE_URL"},
				{Key: "dsn", Label: "DSN", Type: schema.FieldTypeString, Description: "Literal database DSN (overrides dsn_env)", Sensitive: true},
				{Key: "timeout", Label: "Timeout", Type: schema.FieldTypeDuration, Description: "Operation timeout (e.g. 5m)", DefaultValue: "5m"},
			},
			Outputs: []schema.StepOutputDef{
				{Key: "current", Type: "string", Description: "Current applied migration version"},
				{Key: "pending", Type: "int", Description: "Number of pending (unapplied) migrations"},
				{Key: "dirty", Type: "bool", Description: "Whether the database is in a dirty migration state"},
			},
		},
		{
			Type:        "step.migrate_to",
			Plugin:      "workflow-plugin-migrations",
			Description: "Migrates the database to a specific version (up or down as needed).",
			ConfigFields: []schema.ConfigFieldDef{
				{Key: "source_dir", Label: "Source Directory", Type: schema.FieldTypeString, Description: "Directory containing migration files", Required: true},
				{Key: "target", Label: "Target Version", Type: schema.FieldTypeString, Description: "Target migration version to migrate to", Required: true},
				{Key: "driver", Label: "Driver", Type: schema.FieldTypeSelect, Description: "Migration driver to use", Options: []string{"golang-migrate", "goose"}, DefaultValue: "golang-migrate"},
				{Key: "dsn_env", Label: "DSN Env Var", Type: schema.FieldTypeString, Description: "Environment variable name holding the database DSN", DefaultValue: "DATABASE_URL"},
				{Key: "dsn", Label: "DSN", Type: schema.FieldTypeString, Description: "Literal database DSN (overrides dsn_env)", Sensitive: true},
				{Key: "timeout", Label: "Timeout", Type: schema.FieldTypeDuration, Description: "Operation timeout (e.g. 5m)", DefaultValue: "5m"},
			},
			Outputs: []schema.StepOutputDef{
				{Key: "applied", Type: "int", Description: "Number of migrations applied or rolled back to reach the target"},
				{Key: "skipped", Type: "int", Description: "Number of migrations skipped"},
				{Key: "duration_ms", Type: "int", Description: "Total execution time in milliseconds"},
			},
		},
	}
}
