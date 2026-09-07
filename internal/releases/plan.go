package releases

import (
	"context"
	"fmt"
	"sort"

	"saxbase/internal/migrations"
	"saxbase/internal/objects"
)

type MigrationPlan struct {
	Version      int64
	Path, Action string
}
type Plan struct {
	CurrentRelease, TargetRelease string
	CurrentSchema, TargetSchema   int64
	Migrations                    []MigrationPlan
	Objects                       []objects.Status
	Blockers                      []string
}

// Small read-only interfaces prevent planning from invoking deployment methods.
type MigrationInspector interface {
	Inspect(context.Context) (migrations.Inspection, error)
}
type ObjectInspector interface {
	Inspect(context.Context) (objects.Inspection, error)
	Status(context.Context, []objects.File) ([]objects.Status, error)
	Snapshot(context.Context, string) (objects.Snapshot, error)
}

func BuildPlan(ctx context.Context, manifest Manifest, files []objects.File, goose MigrationInspector, db ObjectInspector) (Plan, error) {
	p := Plan{TargetRelease: manifest.Version, Migrations: make([]MigrationPlan, 0), Blockers: make([]string, 0)}
	version, err := ParseVersion(manifest.Version)
	if err != nil {
		return p, err
	}
	p.TargetSchema = version.Schema
	ordered, orderErr := manifest.OrderedFiles(files)
	if err := orderErr; err != nil {
		p.Blockers = append(p.Blockers, err.Error())
	}
	if orderErr == nil {
		files = ordered
	}
	schema, err := goose.Inspect(ctx)
	if err != nil {
		return p, fmt.Errorf("inspect migrations: %w", err)
	}
	p.CurrentSchema = schema.Version
	state, err := db.Inspect(ctx)
	if err != nil {
		return p, fmt.Errorf("inspect releases: %w", err)
	}
	p.CurrentRelease = state.Current
	p.Objects, err = db.Status(ctx, files)
	if err != nil {
		return p, fmt.Errorf("inspect objects: %w", err)
	}
	if manifest.Parent != "" && len(p.Objects) > len(files) {
		p.Objects = p.Objects[:len(files)]
	}
	if version.Schema < schema.Version {
		p.Blockers = append(p.Blockers, fmt.Sprintf("target Goose version %d is below current %d; use rollback", version.Schema, schema.Version))
	}
	targetKnown := version.Schema == 0
	for _, migration := range schema.Migrations {
		if migration.Version == version.Schema {
			targetKnown = true
		}
		action := migration.State
		switch migration.State {
		case "pending":
			action = "apply"
			if migration.Version > version.Schema {
				action = "deferred"
			} else if migration.Version < schema.Version {
				action = "out-of-order"
				p.Blockers = append(p.Blockers, fmt.Sprintf("migration %d is pending below current Goose version %d", migration.Version, schema.Version))
			}
		case "missing":
			p.Blockers = append(p.Blockers, fmt.Sprintf("migration file for applied version %d is missing", migration.Version))
		}
		p.Migrations = append(p.Migrations, MigrationPlan{Version: migration.Version, Path: migration.Path, Action: action})
	}
	if !targetKnown {
		p.Blockers = append(p.Blockers, fmt.Sprintf("target Goose version %d has no migration file or applied record", version.Schema))
	}
	for _, row := range p.Objects {
		if row.State == "missing" && manifest.Parent == "" {
			p.Blockers = append(p.Blockers, fmt.Sprintf("release omits tracked object %s; object removal must be handled explicitly", row.Path))
		}
	}
	for _, rollback := range state.Rollbacks {
		if rollback.Status != "completed" {
			p.Blockers = append(p.Blockers, fmt.Sprintf("rollback to %s is incomplete; retry rollback %s", rollback.TargetVersion, rollback.TargetVersion))
		}
	}
	for _, record := range state.History {
		if record.SchemaVersion > version.Schema || (record.SchemaVersion == version.Schema && record.Revision > version.Revision) {
			p.Blockers = append(p.Blockers, fmt.Sprintf("release %s is older than recorded release %s; use rollback", manifest.Version, record.Version))
			break
		}
	}
	for _, record := range state.History {
		if record.Version == manifest.Version {
			snapshot, err := db.Snapshot(ctx, manifest.Version)
			if err != nil {
				return p, fmt.Errorf("inspect release fingerprint: %w", err)
			}
			if !matchesSnapshot(snapshot, files) {
				p.Blockers = append(p.Blockers, fmt.Sprintf("release %s is immutable: the object order, set, or definitions differ", manifest.Version))
			}
			break
		}
	}
	sort.Slice(p.Migrations, func(i, j int) bool { return p.Migrations[i].Version < p.Migrations[j].Version })
	positions := make(map[string]int, len(manifest.Objects))
	for i, object := range manifest.Objects {
		positions[object.Path] = i
	}
	sort.SliceStable(p.Objects, func(i, j int) bool {
		a, aok := positions[p.Objects[i].Path]
		b, bok := positions[p.Objects[j].Path]
		if aok != bok {
			return aok
		}
		if aok {
			return a < b
		}
		return p.Objects[i].Path < p.Objects[j].Path
	})
	return p, nil
}

func matchesSnapshot(snapshot objects.Snapshot, files []objects.File) bool {
	return snapshot.Fingerprint == objects.Fingerprint(files)
}
