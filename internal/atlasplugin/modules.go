package atlasplugin

import (
	"context"
	"fmt"
	"os"
	"time"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"

	atlasdriver "github.com/GoCodeAlone/workflow-plugin-migrations/internal/atlas"
	"github.com/GoCodeAlone/workflow/interfaces"
)

// atlasMigrationsModule is a sdk.ModuleInstance for database.migrations backed by Atlas.
type atlasMigrationsModule struct {
	name      string
	sourceDir string
	dsnEnv    string
	timeout   time.Duration
}

func newAtlasMigrationsModule(name string, rawCfg map[string]any) sdk.ModuleInstance {
	m := &atlasMigrationsModule{
		name:    name,
		dsnEnv:  "DATABASE_URL",
		timeout: 5 * time.Minute,
	}
	if v, ok := rawCfg["source_dir"].(string); ok {
		m.sourceDir = v
	}
	if v, ok := rawCfg["dsn_env"].(string); ok && v != "" {
		m.dsnEnv = v
	}
	if v, ok := rawCfg["timeout"].(string); ok {
		if d, err := time.ParseDuration(v); err == nil {
			m.timeout = d
		}
	}
	return m
}

func (m *atlasMigrationsModule) Init() error {
	if m.sourceDir == "" {
		return fmt.Errorf("database.migrations %q: source_dir is required", m.name)
	}
	return nil
}

func (m *atlasMigrationsModule) Start(_ context.Context) error { return nil }
func (m *atlasMigrationsModule) Stop(_ context.Context) error  { return nil }

func (m *atlasMigrationsModule) InvokeMethod(method string, args map[string]any) (map[string]any, error) {
	dsn := ""
	if v, ok := args["dsn"].(string); ok && v != "" {
		dsn = v
	}
	if dsn == "" && m.dsnEnv != "" {
		dsn = os.Getenv(m.dsnEnv)
	}
	if dsn == "" {
		return nil, fmt.Errorf("database.migrations %q: no DSN available", m.name)
	}

	sourceDir := m.sourceDir
	if v, ok := args["source_dir"].(string); ok && v != "" {
		sourceDir = v
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	d := atlasdriver.New()
	req := interfaces.MigrationRequest{
		DSN:    dsn,
		Source: interfaces.MigrationSource{Dir: sourceDir},
	}

	switch method {
	case "up":
		result, err := d.Up(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{"applied": result.Applied, "duration_ms": result.DurationMs}, nil
	case "down":
		steps := 1
		if v, ok := args["steps"].(int); ok {
			steps = v
		}
		req.Options.Steps = steps
		result, err := d.Down(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{"applied": result.Applied, "duration_ms": result.DurationMs}, nil
	case "status":
		st, err := d.Status(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{"current": st.Current, "pending": st.Pending, "dirty": st.Dirty}, nil
	case "goto":
		target, _ := args["target"].(string)
		if target == "" {
			return nil, fmt.Errorf("goto: target version is required")
		}
		result, err := d.Goto(ctx, req, target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"applied": result.Applied, "duration_ms": result.DurationMs}, nil
	default:
		return nil, fmt.Errorf("database.migrations %q: unknown method %q", m.name, method)
	}
}

// driverModule is a sdk.ModuleInstance for database.migration_driver (atlas).
type driverModule struct {
	driver interfaces.MigrationDriver
}

func (m *driverModule) Init() error                   { return nil }
func (m *driverModule) Start(_ context.Context) error { return nil }
func (m *driverModule) Stop(_ context.Context) error  { return nil }
func (m *driverModule) InvokeMethod(method string, _ map[string]any) (map[string]any, error) {
	if method == "driver_name" {
		return map[string]any{"name": m.driver.Name()}, nil
	}
	return nil, fmt.Errorf("database.migration_driver: unknown method %q", method)
}

// driverBackedModule wraps a non-Atlas driver (golang-migrate, goose) as a module.
type driverBackedModule struct {
	name   string
	rawCfg map[string]any
	driver interfaces.MigrationDriver
}

func newDriverBackedModule(name string, rawCfg map[string]any, d interfaces.MigrationDriver) sdk.ModuleInstance {
	return &driverBackedModule{name: name, rawCfg: rawCfg, driver: d}
}

func (m *driverBackedModule) Init() error                   { return nil }
func (m *driverBackedModule) Start(_ context.Context) error { return nil }
func (m *driverBackedModule) Stop(_ context.Context) error  { return nil }

// InvokeMethod is intentionally unimplemented for golang-migrate and goose modules
// created via the atlas binary. Use the main workflow-plugin-migrations binary
// (which ships without Atlas HCL dependencies) for those drivers. This module
// exists only so that CreateModule can return a non-nil instance for config
// validation; actual execution routes through the appropriate binary.
func (m *driverBackedModule) InvokeMethod(method string, _ map[string]any) (map[string]any, error) {
	return nil, fmt.Errorf(
		"database.migrations %q: driver %q methods must be invoked via workflow-plugin-migrations, not workflow-plugin-atlas-migrate",
		m.name, m.driver.Name(),
	)
}
