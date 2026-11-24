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
	schemaStored    string
	schemaCurrent   string
	driftStates     map[string]string
	corrected       []string
}

func migrationPointer(version int64) *int64 { return &version }

func (f *fakeObjects) Close() error { f.closed = true; return nil }
func (f *fakeObjects) Inspect(context.Context) (objects.Inspection, error) {
	f.command = "inspect"
	if f.currentSet {
		return objects.Inspection{Current: f.current, History: f.history}, f.err
	}
	return objects.Inspection{Current: "30.0.0"}, f.err
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
func (f *fakeObjects) Snapshot(_ context.Context, version string) (objects.Snapshot, error) {
	f.command = "show"
	return objects.Snapshot{Release: objects.Release{Version: version}, Objects: []objects.SnapshotObject{{Path: "view.sql", SQL: "SELECT 1;"}}}, f.err
}

func (f *fakeObjects) Drift(_ context.Context, files []objects.File) (objects.DriftReport, error) {
	f.command = "drift"
	stored, current := f.schemaStored, f.schemaCurrent
	if stored == "" {
		stored = "abc"
	}
	if current == "" {
		current = stored
	}
	report := objects.DriftReport{SchemaStored: stored, SchemaCurrent: current, SchemaDiff: objects.DiffFingerprints(stored, current)}
	for _, file := range files {
		state := "ok"
		if f.driftStates != nil {
			if s, ok := f.driftStates[file.Path]; ok {
				state = s
			}
		}
		report.Objects = append(report.Objects, objects.Status{Path: file.Path, State: state, Checksum: file.Checksum})
	}
	return report, f.err
}

func (f *fakeObjects) CorrectSQL(_ context.Context, statements []string) error {
	f.command = "correct"
	f.corrected = append(f.corrected, statements...)
	return f.err
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
