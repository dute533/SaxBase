// Package migrations isolates SaxBase from its structural migration engine.
package migrations

import "context"

type Config struct {
	Driver string
	DSN    string
	Dir    string
}

type Status struct {
	Version int64
	Path    string
	State   string
}

// Engine preserves Goose semantics: Up applies all pending migrations and Down
// rolls back one migration. Version is the database's structural version.
type Engine interface {
	Up(context.Context) error
	Down(context.Context) error
	Status(context.Context) ([]Status, error)
	Version(context.Context) (int64, error)
	Close() error
}
