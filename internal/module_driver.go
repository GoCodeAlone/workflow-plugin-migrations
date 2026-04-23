package internal

import (
	"context"
	"fmt"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"
)

// driverModuleConfig is the configuration for the database.migration_driver module.
type driverModuleConfig struct {
	// Driver is the driver name: golang-migrate, goose, atlas.
	Driver string `json:"driver"`
}

// driverModule implements sdk.ModuleInstance for database.migration_driver.
// It declares which driver implementation this module provides.
type driverModule struct {
	name string
	cfg  driverModuleConfig
}

func newDriverModule(name string, rawCfg map[string]any) (sdk.ModuleInstance, error) {
	cfg := driverModuleConfig{}
	if v, ok := rawCfg["driver"].(string); ok {
		cfg.Driver = v
	}
	if cfg.Driver == "" {
		return nil, fmt.Errorf("database.migration_driver %q: driver is required (golang-migrate|goose|atlas)", name)
	}
	switch cfg.Driver {
	case "golang-migrate", "goose", "atlas":
		// valid
	default:
		return nil, fmt.Errorf("database.migration_driver %q: unknown driver %q", name, cfg.Driver)
	}
	return &driverModule{name: name, cfg: cfg}, nil
}

func (m *driverModule) Init() error                    { return nil }
func (m *driverModule) Start(_ context.Context) error  { return nil }
func (m *driverModule) Stop(_ context.Context) error   { return nil }

// InvokeMethod returns driver metadata.
func (m *driverModule) InvokeMethod(method string, _ map[string]any) (map[string]any, error) {
	switch method {
	case "driver_name":
		return map[string]any{"driver": m.cfg.Driver}, nil
	default:
		return nil, fmt.Errorf("database.migration_driver %q: unknown method %q", m.name, method)
	}
}
