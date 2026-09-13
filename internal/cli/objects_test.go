package cli

import (
	"context"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

type fakeObjects struct {
	command        string
	files          []objects.File
	closed         bool
	err            error
	rollbackErr    error
	version        string
	current        string
	currentSet     bool
	rollbackSource string
	rollbackTarget string
	rollbacks      []objects.Rollback
	history        []objects.Release
	force          bool
}

func (f *fakeObjects) Close() error { f.closed = true; return nil }
func (f *fakeObjects) Inspect(context.Context) (objects.Inspection, error) {
	f.command = "inspect"
	if f.currentSet {
		return objects.Inspection{Current: f.current, History: f.history, Rollbacks: f.rollbacks}, f.err
	}
	return objects.Inspection{Current: "30"}, f.err
}
func (f *fakeObjects) Rollback(_ context.Context, target, source objects.Snapshot, _ migrations.Engine) (objects.Rollback, error) {
	f.command = "rollback"
	f.rollbackSource, f.rollbackTarget = source.Version, target.Version
	return objects.Rollback{SourceVersion: source.Version, TargetVersion: target.Version, Status: "completed"}, f.rollbackErr
}
func (f *fakeObjects) Rollbacks(context.Context) ([]objects.Rollback, error) {
	f.command = "rollbacks"
	return []objects.Rollback{}, f.err
}
func (f *fakeObjects) Current(context.Context) (string, error) {
	f.command = "current"
	if f.currentSet {
		return f.current, f.err
	}
	return "30.1", f.err
}

func (f *fakeObjects) History(context.Context) ([]objects.Release, error) {
	f.command = "history"
	return []objects.Release{{Version: "30.1"}}, f.err
}
func (f *fakeObjects) Snapshot(_ context.Context, version string) (objects.Snapshot, error) {
	f.command = "show"
	return objects.Snapshot{Release: objects.Release{Version: version}, Objects: []objects.SnapshotObject{{Path: "view.sql", SQL: "SELECT 1;"}}}, f.err
}

func (f *fakeObjects) Apply(ctx context.Context, files, _ []objects.File, version string, force bool, goose migrations.Engine, preflight func(context.Context) error) ([]objects.Status, error) {
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	f.command = "apply"
	f.files = files
	f.force = force
	f.version = version
	f.currentSet = true
	f.current = version
	return []objects.Status{{Path: files[0].Path, State: "applied", Checksum: files[0].Checksum}}, f.err
}
