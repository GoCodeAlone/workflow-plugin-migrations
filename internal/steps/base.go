// Package steps provides pipeline step implementations for database migrations.
package steps

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

// stepConfig holds the shared configuration for all migrate steps.
type stepConfig struct {
	// Driver to use: golang-migrate (default) or goose.
	Driver string `json:"driver"`
	// SourceDir is the directory containing migration files.
	SourceDir string `json:"source_dir"`
	// DSNEnv is the environment variable holding the database connection string.
	DSNEnv string `json:"dsn_env"`
	// DSN is a literal DSN (overrides DSNEnv; useful in tests).
	DSN string `json:"dsn"`
	// Timeout for the migration operation. Default: 5m.
	Timeout string `json:"timeout"`
}

// parseStepConfig parses a raw config map into a stepConfig.
func parseStepConfig(raw map[string]any) stepConfig {
	cfg := stepConfig{
		Driver:  "golang-migrate",
		DSNEnv:  "DATABASE_URL",
		Timeout: "5m",
	}
	if v, ok := raw["driver"].(string); ok && v != "" {
		cfg.Driver = v
	}
	if v, ok := raw["source_dir"].(string); ok {
		cfg.SourceDir = v
	}
	if v, ok := raw["dsn_env"].(string); ok && v != "" {
		cfg.DSNEnv = v
	}
	if v, ok := raw["dsn"].(string); ok {
		cfg.DSN = v
	}
	if v, ok := raw["timeout"].(string); ok && v != "" {
		cfg.Timeout = v
	}
	return cfg
}

// resolveDSN returns the DSN from the step config, falling back to env var.
func (c stepConfig) resolveDSN() string {
	if c.DSN != "" {
		return c.DSN
	}
	return os.Getenv(c.DSNEnv)
}

// buildDriver instantiates the named migration driver.
func buildDriver(name string) (driver.Driver, error) {
	switch name {
	case "golang-migrate", "":
		return golangmigrate.New(), nil
	case "goose":
		return goose.New(), nil
	default:
		return nil, fmt.Errorf("unknown driver %q", name)
	}
}

// buildRequest constructs a MigrationRequest from a stepConfig.
func buildRequest(cfg stepConfig) (driver.Request, time.Duration, error) {
	dsn := cfg.resolveDSN()
	if dsn == "" {
		return driver.Request{}, 0, fmt.Errorf("no DSN available (set %s env var or dsn config)", cfg.DSNEnv)
	}
	if cfg.SourceDir == "" {
		return driver.Request{}, 0, fmt.Errorf("source_dir is required")
	}
	timeout := 5 * time.Minute
	if t, err := time.ParseDuration(cfg.Timeout); err == nil {
		timeout = t
	}
	req := driver.Request{
		DSN: dsn,
		Source: driver.Source{
			Dir: cfg.SourceDir,
		},
		Options: driver.Options{
			Timeout: timeout,
		},
	}
	return req, timeout, nil
}

// resultToOutput converts a MigrationResult to a step output map.
func resultToOutput(r driver.Result) map[string]any {
	return map[string]any{
		"applied":     r.Applied,
		"skipped":     r.Skipped,
		"duration_ms": r.DurationMs,
	}
}

// MigrateUpStep executes all pending migrations (up).
type MigrateUpStep struct {
	name string
	cfg  stepConfig
}

// NewMigrateUpStep returns a new step.migrate_up step.
func NewMigrateUpStep(name string, raw map[string]any) sdk.StepInstance {
	return &MigrateUpStep{name: name, cfg: parseStepConfig(raw)}
}

// Execute applies all pending migrations.
func (s *MigrateUpStep) Execute(ctx context.Context, _ map[string]any, _ map[string]map[string]any, _ map[string]any, _ map[string]any, raw map[string]any) (*sdk.StepResult, error) {
	cfg := s.cfg
	// Allow runtime overrides via step config pass-through.
	if raw != nil {
		merged := parseStepConfig(raw)
		if merged.SourceDir != "" {
			cfg.SourceDir = merged.SourceDir
		}
		if merged.DSN != "" {
			cfg.DSN = merged.DSN
		}
	}

	d, err := buildDriver(cfg.Driver)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_up %s: %w", s.name, err)
	}
	req, timeout, err := buildRequest(cfg)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_up %s: %w", s.name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := d.Up(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_up %s: %w", s.name, err)
	}
	return &sdk.StepResult{Output: resultToOutput(result)}, nil
}

// MigrateDownStep rolls back N migrations (down).
type MigrateDownStep struct {
	name  string
	cfg   stepConfig
	steps int
}

// NewMigrateDownStep returns a new step.migrate_down step.
func NewMigrateDownStep(name string, raw map[string]any) sdk.StepInstance {
	steps := 1
	if v, ok := raw["steps"].(int); ok && v > 0 {
		steps = v
	}
	return &MigrateDownStep{name: name, cfg: parseStepConfig(raw), steps: steps}
}

// Execute rolls back N migrations.
func (s *MigrateDownStep) Execute(ctx context.Context, _ map[string]any, _ map[string]map[string]any, _ map[string]any, _ map[string]any, _ map[string]any) (*sdk.StepResult, error) {
	d, err := buildDriver(s.cfg.Driver)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_down %s: %w", s.name, err)
	}
	req, timeout, err := buildRequest(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_down %s: %w", s.name, err)
	}
	req.Options.Steps = s.steps

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := d.Down(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_down %s: %w", s.name, err)
	}
	return &sdk.StepResult{Output: resultToOutput(result)}, nil
}

// MigrateStatusStep returns the current migration status.
type MigrateStatusStep struct {
	name string
	cfg  stepConfig
}

// NewMigrateStatusStep returns a new step.migrate_status step.
func NewMigrateStatusStep(name string, raw map[string]any) sdk.StepInstance {
	return &MigrateStatusStep{name: name, cfg: parseStepConfig(raw)}
}

// Execute returns the migration status.
func (s *MigrateStatusStep) Execute(ctx context.Context, _ map[string]any, _ map[string]map[string]any, _ map[string]any, _ map[string]any, _ map[string]any) (*sdk.StepResult, error) {
	d, err := buildDriver(s.cfg.Driver)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_status %s: %w", s.name, err)
	}
	req, timeout, err := buildRequest(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_status %s: %w", s.name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	status, err := d.Status(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_status %s: %w", s.name, err)
	}
	return &sdk.StepResult{Output: map[string]any{
		"current": status.Current,
		"pending": status.Pending,
		"dirty":   status.Dirty,
	}}, nil
}

// MigrateToStep migrates to a specific version.
type MigrateToStep struct {
	name   string
	cfg    stepConfig
	target string
}

// NewMigrateToStep returns a new step.migrate_to step.
func NewMigrateToStep(name string, raw map[string]any) sdk.StepInstance {
	target, _ := raw["target"].(string)
	return &MigrateToStep{name: name, cfg: parseStepConfig(raw), target: target}
}

// Execute migrates the database to the given target version.
func (s *MigrateToStep) Execute(ctx context.Context, _ map[string]any, _ map[string]map[string]any, _ map[string]any, _ map[string]any, _ map[string]any) (*sdk.StepResult, error) {
	if s.target == "" {
		return nil, fmt.Errorf("step.migrate_to %s: target version is required", s.name)
	}
	d, err := buildDriver(s.cfg.Driver)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_to %s: %w", s.name, err)
	}
	req, timeout, err := buildRequest(s.cfg)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_to %s: %w", s.name, err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := d.Goto(ctx, req, s.target)
	if err != nil {
		return nil, fmt.Errorf("step.migrate_to %s: %w", s.name, err)
	}
	return &sdk.StepResult{Output: resultToOutput(result)}, nil
}
