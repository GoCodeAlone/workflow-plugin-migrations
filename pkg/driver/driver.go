// Package driver provides the MigrationDriver adapter and in-process registry.
package driver

import (
	"context"

	"github.com/GoCodeAlone/workflow/interfaces"
)

// Driver is a thin alias so callers can import from this package
// without directly depending on the interfaces package.
type Driver = interfaces.MigrationDriver

// Request is an alias for interfaces.MigrationRequest.
type Request = interfaces.MigrationRequest

// Source is an alias for interfaces.MigrationSource.
type Source = interfaces.MigrationSource

// Options is an alias for interfaces.MigrationOptions.
type Options = interfaces.MigrationOptions

// Result is an alias for interfaces.MigrationResult.
type Result = interfaces.MigrationResult

// Status is an alias for interfaces.MigrationStatus.
type Status = interfaces.MigrationStatus

// Adapter wraps any MigrationDriver and adds optional pre/post hooks.
type Adapter struct {
	inner Driver
}

// NewAdapter wraps a MigrationDriver in an Adapter.
func NewAdapter(d Driver) *Adapter {
	return &Adapter{inner: d}
}

// Name delegates to the inner driver.
func (a *Adapter) Name() string { return a.inner.Name() }

// Up delegates to the inner driver.
func (a *Adapter) Up(ctx context.Context, req Request) (Result, error) {
	return a.inner.Up(ctx, req)
}

// Down delegates to the inner driver.
func (a *Adapter) Down(ctx context.Context, req Request) (Result, error) {
	return a.inner.Down(ctx, req)
}

// Status delegates to the inner driver.
func (a *Adapter) Status(ctx context.Context, req Request) (Status, error) {
	return a.inner.Status(ctx, req)
}

// Goto delegates to the inner driver.
func (a *Adapter) Goto(ctx context.Context, req Request, target string) (Result, error) {
	return a.inner.Goto(ctx, req, target)
}
