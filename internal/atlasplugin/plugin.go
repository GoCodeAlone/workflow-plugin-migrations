// Package atlasplugin provides the Atlas migration driver plugin.
// Atlas has larger dependencies (HCL toolchain) so it ships as a separate binary
// to keep the main workflow-plugin-migrations binary lean.
package atlasplugin

import (
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"

	atlasdriver "github.com/GoCodeAlone/workflow-plugin-migrations/internal/atlas"
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/goose"
)

// Version is set at build time via -ldflags.
var Version = "0.0.0"

// AtlasPlugin implements sdk.PluginProvider for the Atlas driver.
type AtlasPlugin struct{}

// NewPlugin returns a new AtlasPlugin.
func NewPlugin() sdk.PluginProvider {
	return &AtlasPlugin{}
}

// Manifest returns plugin metadata.
func (p *AtlasPlugin) Manifest() sdk.PluginManifest {
	return sdk.PluginManifest{
		Name:        "workflow-plugin-atlas-migrate",
		Version:     Version,
		Author:      "GoCodeAlone",
		Description: "Atlas migration driver for the workflow engine (also ships golang-migrate + goose)",
	}
}

// ModuleTypes returns the module types this plugin provides.
func (p *AtlasPlugin) ModuleTypes() []string {
	return []string{"database.migrations", "database.migration_driver"}
}

// CreateModule creates a module instance for the requested type.
// This binary ships all three drivers (golang-migrate, goose, atlas) but the
// atlas driver is the primary addition over the main plugin binary.
func (p *AtlasPlugin) CreateModule(typeName, name string, rawCfg map[string]any) (sdk.ModuleInstance, error) {
	switch typeName {
	case "database.migrations":
		driverName, _ := rawCfg["driver"].(string)
		if driverName == "" {
			driverName, _ = rawCfg["driver_ref"].(string)
		}
		if driverName == "" {
			driverName = "atlas"
		}
		switch driverName {
		case "atlas":
			return newAtlasMigrationsModule(name, rawCfg), nil
		case "golang-migrate":
			return newDriverBackedModule(name, rawCfg, golangmigrate.New()), nil
		case "goose":
			return newDriverBackedModule(name, rawCfg, goose.New()), nil
		default:
			return nil, fmt.Errorf("atlasplugin: unknown driver %q (supported: atlas, golang-migrate, goose)", driverName)
		}
	case "database.migration_driver":
		return &driverModule{driver: atlasdriver.New()}, nil
	default:
		return nil, fmt.Errorf("atlasplugin: unknown module type %q", typeName)
	}
}

// StepTypes returns nil — steps are handled by the main plugin binary.
func (p *AtlasPlugin) StepTypes() []string { return nil }

// CreateStep returns an error — use the main plugin binary for pipeline steps.
func (p *AtlasPlugin) CreateStep(typeName, name string, _ map[string]any) (sdk.StepInstance, error) {
	return nil, fmt.Errorf("atlasplugin: pipeline steps are provided by workflow-plugin-migrations, not workflow-plugin-atlas-migrate")
}
