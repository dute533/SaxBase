package cli

import (
	"context"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

type fakeObjects struct {
	command         string
	files           []objects.File
	closed          bool
	err             error
	rollbackErr     error
	version         string
	current         string
	currentSet      bool
	rollbackSource  string
	rollbackTarget  string
	rollbackSteps   []string
	unreconciled    bool
	reconciled      int
	history         []objects.Release
	force           bool
	appliedVersions []string
	applyErrAt      string
	applyErr        error
}

func (f *fakeObjects) Close() error { f.closed = true; return nil }
func (f *fakeObjects) Inspect(context.Context) (objects.Inspection, error) {
	f.command = "inspect"
	if f.currentSet {
		return objects.Inspection{Current: f.current, History: f.history}, f.err
	}
	return objects.Inspection{Current: "30"}, f.err
}
func (f *fakeObjects) Rollback(_ context.Context, target, source objects.Snapshot, _ migrations.Engine) (objects.Rollback, error) {
	f.command = "rollback"
	f.rollbackSource, f.rollbackTarget = source.Version, target.Version
	f.rollbackSteps = append(f.rollbackSteps, source.Version+"->"+target.Version)
	f.currentSet = true
	f.current = target.Version
	return objects.Rollback{SourceVersion: source.Version, TargetVersion: target.Version}, f.rollbackErr
}
func (f *fakeObjects) Reconcile(_ context.Context, target objects.Snapshot, _ migrations.Engine) error {
	f.command = "reconcile"
	f.reconciled++
	f.currentSet = true
	f.current = target.Version
	return f.err
}
func (f *fakeObjects) Reconciled(context.Context, objects.Snapshot) (bool, error) {
	return !f.unreconciled, f.err
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

func (f *fakeObjects) Apply(ctx context.Context, files, _ []objects.File, release objects.Release, force bool, goose migrations.Engine, preflight func(context.Context) error) ([]objects.Status, error) {
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	if release.Version == f.applyErrAt {
		return nil, f.applyErr
	}
	f.command = "apply"
	f.files = files
	f.force = force
	f.version = release.Version
	f.currentSet = true
	f.current = release.Version
	f.appliedVersions = append(f.appliedVersions, release.Version)
	rows := make([]objects.Status, 0, len(files))
	for _, file := range files {
		rows = append(rows, objects.Status{Path: file.Path, State: "applied", Checksum: file.Checksum})
	}
	return rows, f.err
}
