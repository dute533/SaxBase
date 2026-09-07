package releases

import (
	"context"
	"errors"
	"strings"
	"testing"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

type planSchema struct {
	state migrations.Inspection
	err   error
}

func (s planSchema) Inspect(context.Context) (migrations.Inspection, error) { return s.state, s.err }

type planObjects struct {
	state    objects.Inspection
	rows     []objects.Status
	snapshot objects.Snapshot
	err      error
}

func (s planObjects) Inspect(context.Context) (objects.Inspection, error) { return s.state, s.err }
func (s planObjects) Status(context.Context, []objects.File) ([]objects.Status, error) {
	return s.rows, s.err
}
func (s planObjects) Snapshot(context.Context, string) (objects.Snapshot, error) {
	return s.snapshot, s.err
}

func TestPlanMigrationBoundaries(t *testing.T) {
	manifest, _ := New("2", nil)
	schema := planSchema{state: migrations.Inspection{Migrations: []migrations.Status{{Version: 3, State: "pending"}, {Version: 1, State: "pending"}, {Version: 2, State: "pending"}}}}
	p, err := BuildPlan(context.Background(), manifest, nil, schema, planObjects{})
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	if p.CurrentSchema != 0 || p.TargetSchema != 2 || p.Migrations[0].Action != "apply" || p.Migrations[1].Action != "apply" || p.Migrations[2].Action != "deferred" {
		t.Fatalf("bad boundary: %+v", p)
	}
}

func TestPlanBlockers(t *testing.T) {
	file := objects.File{Commit: strings.Repeat("a", 40), Path: "view.sql", SQL: "SELECT 1;", Checksum: strings.Repeat("a", 64)}
	manifest, _ := New("2.1", []objects.File{file})
	base := migrations.Inspection{Version: 2, Migrations: []migrations.Status{{Version: 2, State: "applied"}}}
	for _, tc := range []struct {
		name, want string
		schema     migrations.Inspection
		db         planObjects
		files      []objects.File
	}{
		{name: "stale file", want: "commit mismatch", schema: base, files: []objects.File{{Path: file.Path, Checksum: "bad"}}},
		{name: "downgrade", want: "below current", schema: migrations.Inspection{Version: 3, Migrations: base.Migrations}, files: []objects.File{file}},
		{name: "unknown target", want: "no migration", schema: migrations.Inspection{}, files: []objects.File{file}},
		{name: "out of order", want: "pending below", schema: migrations.Inspection{Version: 2, Migrations: []migrations.Status{{Version: 1, State: "pending"}, {Version: 2, State: "applied"}}}, files: []objects.File{file}},
		{name: "missing migration", want: "migration file", schema: migrations.Inspection{Version: 2, Migrations: []migrations.Status{{Version: 2, State: "missing"}}}, files: []objects.File{file}},
		{name: "missing object", want: "omits tracked", schema: base, files: []objects.File{file}, db: planObjects{rows: []objects.Status{{Path: "old.sql", State: "missing"}}}},
		{name: "rollback", want: "rollback to 2 is incomplete", schema: base, files: []objects.File{file}, db: planObjects{state: objects.Inspection{Rollbacks: []objects.Rollback{{TargetVersion: "2", Status: "failed"}}}}},
		{name: "older revision", want: "older than recorded", schema: base, files: []objects.File{file}, db: planObjects{state: objects.Inspection{History: []objects.Release{{Version: "2.10", SchemaVersion: 2, Revision: 10}}}}},
		{name: "immutable", want: "immutable", schema: base, files: []objects.File{file}, db: planObjects{state: objects.Inspection{History: []objects.Release{{Version: "2.1", SchemaVersion: 2, Revision: 1}}}, snapshot: objects.Snapshot{Objects: []objects.SnapshotObject{{Path: file.Path, Checksum: file.Checksum, SQL: "different SQL"}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := BuildPlan(context.Background(), manifest, tc.files, planSchema{state: tc.schema}, tc.db)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(p.Blockers, "\n"), tc.want) {
				t.Fatalf("blockers %v, want %s", p.Blockers, tc.want)
			}
		})
	}
}

func TestPlanUnchangedAndNumericRevision(t *testing.T) {
	m, _ := New("2.10", nil)
	db := planObjects{state: objects.Inspection{Current: "2.2", History: []objects.Release{{Version: "2.2", SchemaVersion: 2, Revision: 2}}}}
	schema := planSchema{state: migrations.Inspection{Version: 2, Migrations: []migrations.Status{{Version: 2, State: "applied"}}}}
	p, err := BuildPlan(context.Background(), m, nil, schema, db)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("%+v %v", p, err)
	}
	db.state.History = []objects.Release{{Version: "2.10", SchemaVersion: 2, Revision: 10}}
	db.snapshot = objects.Snapshot{Release: objects.Release{Fingerprint: objects.Fingerprint(nil)}, Objects: []objects.SnapshotObject{}}
	p, err = BuildPlan(context.Background(), m, nil, schema, db)
	if err != nil || len(p.Blockers) != 0 || p.Migrations[0].Action != "applied" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestPlanInspectionFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	m, _ := New("0", nil)
	if _, err := BuildPlan(context.Background(), m, nil, planSchema{err: failure}, planObjects{}); !errors.Is(err, failure) {
		t.Fatalf("lost error: %v", err)
	}
}
