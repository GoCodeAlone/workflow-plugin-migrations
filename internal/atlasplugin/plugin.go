// Package atlasplugin provides the Atlas migration driver plugin.
// Atlas has larger dependencies (HCL toolchain) so it ships as a separate binary.
// This implementation will be fleshed out in Task 27.
package atlasplugin

import (
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
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
		Description: "Atlas HCL migration driver for the workflow engine",
	}
}

// ModuleTypes returns the module types this plugin provides.
func (p *AtlasPlugin) ModuleTypes() []string {
	return []string{"database.migrations"}
}

// CreateModule creates a module instance (atlas driver only).
func (p *AtlasPlugin) CreateModule(typeName, name string, _ map[string]any) (sdk.ModuleInstance, error) {
	return nil, fmt.Errorf("atlasplugin: driver implementation coming in Task 27 (use workflow-plugin-migrations binary for golang-migrate/goose)")
}

// StepTypes returns step type names.
func (p *AtlasPlugin) StepTypes() []string { return nil }

// CreateStep creates a step instance.
func (p *AtlasPlugin) CreateStep(typeName, name string, _ map[string]any) (sdk.StepInstance, error) {
	return nil, fmt.Errorf("atlasplugin: no steps provided")
}
