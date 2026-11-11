// Package migrations isolates SaxBase from its structural migration engine.
package migrations

import (
	"context"

	"saxbase/internal/dialect"
)

type Config struct {
	Dialect dialect.Name
	DSN     string
	Dir     string
}

type Status struct {
	Version int64
	Path    string
	State   string
}

// Engine exposes the migration operations used by the release coordinator.
// Version is the database's structural version.
type Engine interface {
	// UpTo is called by the release coordinator while holding the deployment lock.
	UpTo(context.Context, int64) error
	// DownTo is used by the release coordinator while it holds the deployment lock.
	DownTo(context.Context, int64) error
	ValidateDownTo(context.Context, int64) error
	Inspect(context.Context) (Inspection, error)
	Version(context.Context) (int64, error)
	Close() error
}

type Inspection struct {
	Version    int64
	Migrations []Status
}
