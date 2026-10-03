// Package runner exposes in-process migrations without CLI or process-global state.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/GoCodeAlone/workflow-plugin-migrations/pkg/driver"
	"github.com/GoCodeAlone/workflow/interfaces"
	"github.com/jackc/pgx/v5"
)

// New binds a registered driver and per-instance output. Empty name selects
// golang-migrate; nil writers discard output. Requests supply the DSN explicitly.
// Operations preserve caller cancellation and apply Options.Timeout when positive.
func New(registry *driver.Registry, name string, stdout, stderr io.Writer) (driver.Driver, error) {
	if registry == nil {
		return nil, fmt.Errorf("migration runner: registry is required")
	}
	if name == "" {
		name = "golang-migrate"
	}
	d, err := registry.Get(name)
	if err != nil {
		return nil, err
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &runner{inner: d, out: stdout, diagnostics: stderr}, nil
}

type runner struct {
	inner            driver.Driver
	out, diagnostics io.Writer
}

func (r *runner) Name() string { return r.inner.Name() }

func operationContext(ctx context.Context, req driver.Request) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return ctx, func() {}, err
	}
	if err := req.Validate(); err != nil {
		return ctx, func() {}, err
	}
	if req.Options.Timeout < 0 {
		return ctx, func() {}, fmt.Errorf("%w: negative migration timeout", interfaces.ErrValidation)
	}
	if req.Options.Timeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, req.Options.Timeout)
		return ctx, cancel, nil
	}
	return ctx, func() {}, nil
}

func (r *runner) Up(ctx context.Context, req driver.Request) (driver.Result, error) {
	return r.run(ctx, req, "up", func(ctx context.Context) (driver.Result, error) { return r.inner.Up(ctx, req) })
}
func (r *runner) Down(ctx context.Context, req driver.Request) (driver.Result, error) {
	return r.run(ctx, req, "down", func(ctx context.Context) (driver.Result, error) { return r.inner.Down(ctx, req) })
}
func (r *runner) Goto(ctx context.Context, req driver.Request, target string) (driver.Result, error) {
	return r.run(ctx, req, "goto", func(ctx context.Context) (driver.Result, error) { return r.inner.Goto(ctx, req, target) })
}

func (r *runner) run(ctx context.Context, req driver.Request, op string, fn func(context.Context) (driver.Result, error)) (driver.Result, error) {
	ctx, cancel, err := operationContext(ctx, req)
	defer cancel()
	if err != nil {
		return driver.Result{}, r.failure(req, err)
	}
	result, err := fn(ctx)
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	if err != nil {
		return result, r.failure(req, err)
	}
	_, err = io.WriteString(r.out, sanitize(req, fmt.Sprintf("%s: %d migration(s): %v\n", op, len(result.Applied), result.Applied)))
	if err != nil {
		return result, r.failure(req, err)
	}
	return result, nil
}

func (r *runner) Status(ctx context.Context, req driver.Request) (driver.Status, error) {
	ctx, cancel, err := operationContext(ctx, req)
	defer cancel()
	if err != nil {
		return driver.Status{}, r.failure(req, err)
	}
	st, err := r.inner.Status(ctx, req)
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	if err != nil {
		return st, r.failure(req, err)
	}
	_, err = io.WriteString(r.out, sanitize(req, fmt.Sprintf("Current: %s\nPending: %v\nDirty: %t\n", st.Current, st.Pending, st.Dirty)))
	if err != nil {
		return st, r.failure(req, err)
	}
	return st, nil
}

type diagnosticError struct {
	message string
	cause   error
}

func (e *diagnosticError) Error() string { return e.message }
func (e *diagnosticError) Unwrap() error { return e.cause }

func (r *runner) failure(req driver.Request, cause error) error {
	message := sanitize(req, "migration runner: "+cause.Error())
	if _, err := io.WriteString(r.diagnostics, message+"\n"); err != nil {
		cause = errors.Join(cause, err)
	}
	return &diagnosticError{message: message, cause: cause}
}

// Redact before truncating so a credential crossing the limit cannot leak.
// Diagnostics, including their newline, have a fixed 4 KiB budget.
func sanitize(req driver.Request, message string) string {
	if req.DSN != "" {
		message = strings.ReplaceAll(message, req.DSN, "[redacted DSN]")
		for _, scheme := range []string{"postgres://", "postgresql://", "pgx5://"} {
			if i := strings.Index(req.DSN, "://"); i >= 0 {
				message = strings.ReplaceAll(message, scheme+req.DSN[i+3:], "[redacted DSN]")
			}
		}
		var passwords []string
		if u, err := url.Parse(req.DSN); err == nil {
			if u.User != nil {
				if p, ok := u.User.Password(); ok {
					passwords = append(passwords, p)
				}
			}
			passwords = append(passwords, u.Query()["password"]...)
		}
		if config, err := pgx.ParseConfig(req.DSN); err == nil {
			passwords = append(passwords, config.Password)
		}
		for _, password := range passwords {
			if password != "" {
				for _, form := range []string{password, url.QueryEscape(password), url.PathEscape(password)} {
					message = strings.ReplaceAll(message, form, "[redacted]")
				}
			}
		}
	}
	if len(message) > 4095 {
		message = message[:4092] + "..."
	}
	return message
}
