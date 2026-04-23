package internal

import (
	"context"
	"fmt"
	"os"
	"time"

	sdk "github.com/GoCodeAlone/workflow/plugin/external/sdk"

	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/golangmigrate"
	"github.com/GoCodeAlone/workflow-plugin-migrations/internal/goose"
	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/driver"
)

// migrationsModuleConfig is the configuration for the database.migrations module.
type migrationsModuleConfig struct {
	// DriverRef is the name of a database.migration_driver module, OR a driver
	// name (golang-migrate, goose) for inline driver configuration.
	DriverRef string `json:"driver_ref"`
	// Driver is the driver name when not using a module ref.
	Driver string `json:"driver"`
	// SourceDir is the directory containing migration files.
	SourceDir string `json:"source_dir"`
	// DSNEnv is the environment variable holding the database connection string.
	DSNEnv string `json:"dsn_env"`
	// HistoryTable is the migration history table name (driver default if empty).
	HistoryTable string `json:"history_table"`
	// Timeout for migration operations. Default: 5m.
	Timeout string `json:"timeout"`
}

// migrationsModule implements sdk.ModuleInstance for database.migrations.
type migrationsModule struct {
	name   string
	cfg    migrationsModuleConfig
	driver driver.Driver
}

func newMigrationsModule(name string, rawCfg map[string]any) (sdk.ModuleInstance, error) {
	cfg := migrationsModuleConfig{
		Timeout: "5m",
	}
	if v, ok := rawCfg["driver_ref"].(string); ok {
		cfg.DriverRef = v
	}
	if v, ok := rawCfg["driver"].(string); ok {
		cfg.Driver = v
	}
	if v, ok := rawCfg["source_dir"].(string); ok {
		cfg.SourceDir = v
	}
	if v, ok := rawCfg["dsn_env"].(string); ok {
		cfg.DSNEnv = v
	}
	if v, ok := rawCfg["history_table"].(string); ok {
		cfg.HistoryTable = v
	}
	if v, ok := rawCfg["timeout"].(string); ok {
		cfg.Timeout = v
	}
	if cfg.SourceDir == "" {
		return nil, fmt.Errorf("database.migrations %q: source_dir is required", name)
	}
	if cfg.DSNEnv == "" && rawCfg["dsn"] == nil {
		// Allow direct dsn for test usage
		if v, ok := rawCfg["dsn"].(string); !ok || v == "" {
			cfg.DSNEnv = "DATABASE_URL"
		}
	}
	return &migrationsModule{name: name, cfg: cfg}, nil
}

func (m *migrationsModule) Init() error {
	driverName := m.cfg.Driver
	if driverName == "" && m.cfg.DriverRef != "" {
		// When driver_ref is set to a simple name (not a module reference),
		// treat it as the driver name.
		driverName = m.cfg.DriverRef
	}
	if driverName == "" {
		driverName = "golang-migrate" // sensible default
	}

	switch driverName {
	case "golang-migrate":
		m.driver = golangmigrate.New()
	case "goose":
		m.driver = goose.New()
	default:
		return fmt.Errorf("database.migrations %q: unknown driver %q", m.name, driverName)
	}
	return nil
}

func (m *migrationsModule) Start(_ context.Context) error { return nil }
func (m *migrationsModule) Stop(_ context.Context) error  { return nil }

// InvokeMethod dispatches up/down/status/goto calls to the driver.
func (m *migrationsModule) InvokeMethod(method string, args map[string]any) (map[string]any, error) {
	dsn := m.resolveDSN(args)
	if dsn == "" {
		return nil, fmt.Errorf("database.migrations %q: no DSN available", m.name)
	}
	sourceDir := m.cfg.SourceDir
	if v, ok := args["source_dir"].(string); ok && v != "" {
		sourceDir = v
	}

	timeout := 5 * time.Minute
	if t, err := time.ParseDuration(m.cfg.Timeout); err == nil {
		timeout = t
	}

	req := driver.Request{
		DSN: dsn,
		Source: driver.Source{
			Dir: sourceDir,
		},
		Options: driver.Options{
			Timeout: timeout,
		},
	}

	ctx := context.Background()

	switch method {
	case "up":
		result, err := m.driver.Up(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"applied":     result.Applied,
			"skipped":     result.Skipped,
			"duration_ms": result.DurationMs,
		}, nil
	case "down":
		steps := 1
		if v, ok := args["steps"].(int); ok {
			steps = v
		}
		req.Options.Steps = steps
		result, err := m.driver.Down(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"applied":     result.Applied,
			"skipped":     result.Skipped,
			"duration_ms": result.DurationMs,
		}, nil
	case "status":
		status, err := m.driver.Status(ctx, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"current": status.Current,
			"pending": status.Pending,
			"dirty":   status.Dirty,
		}, nil
	case "goto":
		target, _ := args["target"].(string)
		if target == "" {
			return nil, fmt.Errorf("goto: target version is required")
		}
		result, err := m.driver.Goto(ctx, req, target)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"applied":     result.Applied,
			"skipped":     result.Skipped,
			"duration_ms": result.DurationMs,
		}, nil
	default:
		return nil, fmt.Errorf("database.migrations %q: unknown method %q", m.name, method)
	}
}

func (m *migrationsModule) resolveDSN(args map[string]any) string {
	// 1. Arg override
	if v, ok := args["dsn"].(string); ok && v != "" {
		return v
	}
	// 2. Module config DSN env
	if m.cfg.DSNEnv != "" {
		if v := os.Getenv(m.cfg.DSNEnv); v != "" {
			return v
		}
	}
	return ""
}
