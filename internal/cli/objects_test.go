package cli

import (
	"context"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

type fakeObjects struct {
	command  string
	files    []objects.File
	closed   bool
	err      error
	schema   int64
	revision int64
}

func (f *fakeObjects) Status(_ context.Context, files []objects.File) ([]objects.Status, error) {
	f.command = "status"
	f.files = files
	return []objects.Status{{Path: files[0].Path, State: "new", Checksum: files[0].Checksum}}, f.err
}
func (f *fakeObjects) Close() error { f.closed = true; return nil }
func (f *fakeObjects) Inspect(context.Context) (objects.Inspection, error) {
	return objects.Inspection{Current: "30"}, f.err
}
func (f *fakeObjects) Rollback(_ context.Context, target, source objects.Snapshot, _ migrations.Engine) (objects.Rollback, error) {
	f.command = "rollback"
	return objects.Rollback{SourceVersion: "30.2", TargetVersion: target.Version, Status: "completed"}, f.err
}
func (f *fakeObjects) Rollbacks(context.Context) ([]objects.Rollback, error) {
	f.command = "rollbacks"
	return []objects.Rollback{}, f.err
}
func (f *fakeObjects) Current(context.Context) (string, error) {
	f.command = "current"
	return "30.1", f.err
}

func (f *fakeObjects) History(context.Context) ([]objects.Release, error) {
	f.command = "history"
	return []objects.Release{{Version: "30.1", SchemaVersion: 30, Revision: 1, ObjectCount: 1}}, f.err
}
func (f *fakeObjects) Snapshot(_ context.Context, version string) (objects.Snapshot, error) {
	f.command = "show"
	return objects.Snapshot{Release: objects.Release{Version: version}, Objects: []objects.SnapshotObject{{Path: "view.sql", SQL: "SELECT 1;"}}}, f.err
}

func (f *fakeObjects) Deploy(ctx context.Context, files []objects.File, schema, revision int64, goose migrations.Engine, preflight func(context.Context) error) ([]objects.Status, error) {
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	f.command = "apply"
	f.files = files
	f.schema, f.revision = schema, revision
	return []objects.Status{{Path: files[0].Path, State: "applied", Checksum: files[0].Checksum}}, f.err
}
