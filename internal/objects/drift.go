package objects

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"saxbase/internal/dialect"
)

// DriftReport compares the live database against the recorded release state.
type DriftReport struct {
	SchemaStored  string
	SchemaCurrent string
	SchemaDiff    []string // human-readable changes from SchemaStored to SchemaCurrent
	Objects       []Status
}

// Drifted reports whether the schema fingerprint or any managed object differs.
// Purely added objects (present live, absent from the recorded state) are not
// drift; SchemaDiff already excludes them.
func (r DriftReport) Drifted() bool {
	if len(r.SchemaDiff) > 0 {
		return true
	}
	for _, row := range r.Objects {
		if row.State != "ok" {
			return true
		}
	}
	return false
}

var orAlterRe = regexp.MustCompile(`(?i)^\s*CREATE\s+OR\s+ALTER\s+`)

// NormalizeDefinition canonicalizes an object definition for comparison: it
// strips the repeatable OR ALTER verb, removes semicolons, collapses
// whitespace and lowercases.
func NormalizeDefinition(s string) string {
	s = strings.TrimPrefix(s, "\ufeff")
	s = orAlterRe.ReplaceAllString(s, "CREATE ")
	s = strings.ReplaceAll(s, ";", "")
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

const schemaFingerprintKey = "schema_fingerprint"

// bookkeeping tables are SaxBase/Goose metadata, not user schema.
const bookkeepingFilter = "NOT (s.name = 'dbo' AND t.name IN ('saxbase_schema_version','saxbase_releases','saxbase_meta','migrations'))"

const pgSchemaFilter = "n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\\_%' ESCAPE '\\'"
const pgBookkeeping = pgSchemaFilter + " AND NOT (n.nspname = 'public' AND c.relname IN ('saxbase_schema_version','saxbase_releases','saxbase_meta'))"
const pgTableBookkeeping = "table_schema NOT IN ('pg_catalog','information_schema') AND table_schema NOT LIKE 'pg\\_%' ESCAPE '\\' AND NOT (table_schema = 'public' AND table_name IN ('saxbase_schema_version','saxbase_releases','saxbase_meta'))"

type fingerprintQuery struct{ tag, query string }

// fingerprintQueriesFor returns the canonical catalog queries per dialect.
func fingerprintQueriesFor(name dialect.Name) ([]fingerprintQuery, error) {
	switch name {
	case dialect.SQLServer:
		return sqlserverFingerprintQueries, nil
	case dialect.Postgres:
		return postgresFingerprintQueries, nil
	}
	return nil, fmt.Errorf("drift detection is not implemented for dialect %q", name)
}

var sqlserverFingerprintQueries = []fingerprintQuery{
	{"schema", `SELECT name FROM sys.schemas WHERE name NOT IN ('sys','INFORMATION_SCHEMA','guest') AND name NOT LIKE 'db\_%' ESCAPE '\' ORDER BY name`},
	{"table", `SELECT s.name + '.' + t.name FROM sys.tables t JOIN sys.schemas s ON s.schema_id = t.schema_id WHERE ` + bookkeepingFilter + ` ORDER BY 1`},
	{"column", `SELECT s.name + '.' + t.name + '.' + c.name, ty.name, c.max_length, c.precision, c.scale, c.is_nullable, c.is_identity, ISNULL(dc.definition, ''), ISNULL(cc.definition, '')
	FROM sys.columns c
	JOIN sys.tables t ON t.object_id = c.object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	JOIN sys.types ty ON ty.user_type_id = c.user_type_id
	LEFT JOIN sys.default_constraints dc ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
	LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
	WHERE ` + bookkeepingFilter + ` ORDER BY 1, c.column_id`},
	{"index", `SELECT s.name + '.' + t.name + '.' + i.name, i.is_unique, i.type_desc, ISNULL(i.filter_definition, ''), STRING_AGG(c.name + CASE WHEN ic.is_descending_key = 1 THEN ' DESC' ELSE '' END, ',') WITHIN GROUP (ORDER BY ic.key_ordinal)
	FROM sys.indexes i
	JOIN sys.tables t ON t.object_id = i.object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id AND ic.is_included_column = 0
	JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
	WHERE i.index_id > 0 AND ` + bookkeepingFilter + `
	GROUP BY s.name, t.name, i.name, i.is_unique, i.type_desc, i.filter_definition ORDER BY 1`},
	{"key", `SELECT s.name + '.' + t.name + '.' + kc.name, kc.type_desc, STRING_AGG(c.name, ',') WITHIN GROUP (ORDER BY ic.key_ordinal)
	FROM sys.key_constraints kc
	JOIN sys.tables t ON t.object_id = kc.parent_object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	JOIN sys.index_columns ic ON ic.object_id = kc.parent_object_id AND ic.index_id = kc.unique_index_id
	JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
	WHERE ` + bookkeepingFilter + `
	GROUP BY s.name, t.name, kc.name, kc.type_desc ORDER BY 1`},
	{"fk", `SELECT fk.name, OBJECT_SCHEMA_NAME(fk.parent_object_id) + '.' + OBJECT_NAME(fk.parent_object_id), OBJECT_SCHEMA_NAME(fk.referenced_object_id) + '.' + OBJECT_NAME(fk.referenced_object_id), STRING_AGG(pc.name + '->' + rc.name, ',') WITHIN GROUP (ORDER BY fkc.constraint_column_id)
	FROM sys.foreign_keys fk
	JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
	JOIN sys.columns pc ON pc.object_id = fk.parent_object_id AND pc.column_id = fkc.parent_column_id
	JOIN sys.columns rc ON rc.object_id = fk.referenced_object_id AND rc.column_id = fkc.referenced_column_id
	GROUP BY fk.name, fk.parent_object_id, fk.referenced_object_id ORDER BY 1`},
	{"check", `SELECT s.name + '.' + t.name + '.' + cc.name, cc.definition
	FROM sys.check_constraints cc
	JOIN sys.tables t ON t.object_id = cc.parent_object_id
	JOIN sys.schemas s ON s.schema_id = t.schema_id
	WHERE ` + bookkeepingFilter + ` ORDER BY 1`},
	{"module", `SELECT s.name + '.' + o.name, o.type, m.definition
	FROM sys.sql_modules m
	JOIN sys.objects o ON o.object_id = m.object_id
	JOIN sys.schemas s ON s.schema_id = o.schema_id
	WHERE o.type IN ('V','P','FN','TF','IF','TR') ORDER BY 1`},
}

var postgresFingerprintQueries = []fingerprintQuery{
	{"schema", `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema') AND schema_name NOT LIKE 'pg\_%' ESCAPE '\' ORDER BY 1`},
	{"table", `SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_type = 'BASE TABLE' AND ` + pgTableBookkeeping + ` ORDER BY 1`},
	{"column", `SELECT table_schema || '.' || table_name || '.' || column_name, COALESCE(udt_name, data_type), character_maximum_length, numeric_precision, numeric_scale, is_nullable, is_identity, COALESCE(column_default, '')
	FROM information_schema.columns WHERE ` + pgTableBookkeeping + ` ORDER BY 1, ordinal_position`},
	{"index", `SELECT schemaname || '.' || tablename || '.' || indexname, indexdef FROM pg_indexes
	WHERE schemaname NOT IN ('pg_catalog','information_schema') AND schemaname NOT LIKE 'pg\_%' ESCAPE '\'
	AND NOT (schemaname = 'public' AND tablename IN ('saxbase_schema_version','saxbase_releases','saxbase_meta')) ORDER BY 1`},
	{"key", `SELECT n.nspname || '.' || c.relname || '.' || con.conname, con.contype::text, pg_get_constraintdef(con.oid)
	FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE con.contype IN ('p','u') AND ` + pgBookkeeping + ` ORDER BY 1`},
	{"fk", `SELECT n.nspname || '.' || c.relname || '.' || con.conname, con.contype::text, pg_get_constraintdef(con.oid)
	FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE con.contype = 'f' AND ` + pgBookkeeping + ` ORDER BY 1`},
	{"check", `SELECT n.nspname || '.' || c.relname || '.' || con.conname, con.contype::text, pg_get_constraintdef(con.oid)
	FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE con.contype = 'c' AND ` + pgBookkeeping + ` ORDER BY 1`},
	{"module", `SELECT n.nspname || '.' || c.relname, 'v', pg_get_viewdef(c.oid, true)
	FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relkind = 'v' AND ` + pgSchemaFilter + ` AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e')
	UNION ALL
	SELECT n.nspname || '.' || p.proname, p.prokind::text, pg_get_functiondef(p.oid)
	FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
	WHERE p.prokind IN ('f','p') AND ` + pgSchemaFilter + ` AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e')
	UNION ALL
	SELECT n.nspname || '.' || t.tgname, 'tr', pg_get_triggerdef(t.oid)
	FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE NOT t.tgisinternal AND ` + pgSchemaFilter + ` ORDER BY 1`},
}

type execer interface {
	reader
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// fingerprint renders the canonical schema and module metadata of the database,
// one tagged line per catalog row. Module definitions are stored as a hash so
// the text stays small; diffFingerprints reports them as "definition changed".
// On postgres, search_path is pinned to pg_catalog so pg_get_viewdef renders
// schema-qualified names exactly as written in the object files.
func fingerprint(ctx context.Context, db execer, dialectName dialect.Name) (string, error) {
	queries, err := fingerprintQueriesFor(dialectName)
	if err != nil {
		return "", err
	}
	if dialectName == dialect.Postgres {
		if _, err := db.ExecContext(ctx, "SET search_path TO pg_catalog"); err != nil {
			return "", err
		}
	}
	var b strings.Builder
	for _, q := range queries {
		rows, err := db.QueryContext(ctx, q.query)
		if err != nil {
			return "", err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return "", err
			}
			parts := make([]string, len(cols))
			for i, v := range vals {
				switch value := v.(type) {
				case nil:
					parts[i] = ""
				case string:
					parts[i] = NormalizeDefinition(value)
				case []byte:
					parts[i] = NormalizeDefinition(string(value))
				default:
					parts[i] = fmt.Sprintf("%v", value)
				}
			}
			if q.tag == "module" {
				parts[len(parts)-1] = hashDefinition(parts[len(parts)-1])
			} else if q.tag == "index" || q.tag == "key" || q.tag == "fk" || q.tag == "check" {
				parts[0] = stableName(parts[0])
			}
			fmt.Fprintln(&b, q.tag+"\t"+strings.Join(parts, "\t"))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
	}
	return b.String(), nil
}

func hashDefinition(s string) string {
	sum := sha256.Sum256([]byte(NormalizeDefinition(s)))
	return hex.EncodeToString(sum[:])
}

var autoNameRe = regexp.MustCompile(`(?i)(^|\.)(PK|FK|CK|UQ|DF|IX)__.*$`)

// stableName masks SQL Server auto-generated constraint/index names (e.g.
// PK__customers__3213E83F), which differ per database, so fingerprints are
// stable across databases while named constraints stay distinct.
func stableName(s string) string {
	return autoNameRe.ReplaceAllString(s, "${1}${2}__auto")
}

func lineCounts(s string) map[string]int {
	m := map[string]int{}
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			m[line]++
		}
	}
	return m
}

// moduleIdentity returns "name type" of a module fingerprint line.
func moduleIdentity(line string) string {
	fields := strings.Split(line, "\t")
	if len(fields) >= 3 {
		return fields[1] + " " + fields[2]
	}
	return line
}

// diffFingerprints renders the changes from stored to current as sorted lines:
// "+ ..." added, "- ..." removed, "~ <module> definition changed".
func diffFingerprints(stored, current string) []string {
	oldCount := lineCounts(stored)
	newCount := lineCounts(current)
	var diff []string
	for line := range newCount {
		if oldCount[line] > 0 || !strings.HasPrefix(line, "module\t") {
			continue
		}
		id := moduleIdentity(line)
		for old := range oldCount {
			if oldCount[old] > 0 && strings.HasPrefix(old, "module\t") && moduleIdentity(old) == id {
				oldCount[old]--
				newCount[line]--
				diff = append(diff, "~ "+id+" definition changed")
				break
			}
		}
	}
	for line, n := range newCount {
		for ; n > 0; n-- {
			if oldCount[line] > 0 {
				oldCount[line]--
			} else {
				diff = append(diff, "+ "+line)
			}
		}
	}
	for line, n := range oldCount {
		for ; n > 0; n-- {
			diff = append(diff, "- "+line)
		}
	}
	sort.Strings(diff)
	return diff
}

// DiffFingerprints renders recorded-to-current schema differences, excluding
// purely added (unmanaged) objects.
func DiffFingerprints(stored, current string) []string {
	return filterAdditions(diffFingerprints(stored, current))
}

// diffIdentity identifies what a fingerprint line is about, ignoring its
// value fields: "column\tschema.table.col", "module\tschema.name\ttype", etc.
func diffIdentity(line string) string {
	fields := strings.SplitN(line, "\t", 4)
	if len(fields) < 2 {
		return line
	}
	id := fields[0] + "\t" + fields[1]
	if fields[0] == "module" && len(fields) > 2 {
		id += "\t" + fields[2]
	}
	return id
}

// filterAdditions drops "+ ..." diff lines whose identity has no matching
// "- ..." line. Objects SaxBase does not manage may be added by hand and are
// not drift; modifications (a "+"/"-" pair on the same identity) and removals
// ("-" only) are kept.
func filterAdditions(diff []string) []string {
	removed := map[string]bool{}
	for _, line := range diff {
		if strings.HasPrefix(line, "- ") {
			removed[diffIdentity(line[2:])] = true
		}
	}
	var out []string
	for _, line := range diff {
		if strings.HasPrefix(line, "+ ") && !removed[diffIdentity(line[2:])] {
			continue
		}
		out = append(out, line)
	}
	return out
}

// DiffDescribed renders the differences from a release-described fingerprint
// (replayed in a scratch database) to the live one: "+ ..." exists only in the
// live database, "- ..." only in the release description. Modules pinned by
// managed object files are excluded (Drift compares those per file); all other
// modules, including triggers, are reported.
func DiffDescribed(described, live string, managed []File) []string {
	skip := map[string]bool{}
	for _, f := range managed {
		if id, err := objectIdentity(f.SQL); err == nil {
			skip[strings.ToLower(id.schema+"."+id.name)] = true // keyed on name only: object names are unique per schema
		}
	}
	var out []string
	for _, line := range diffFingerprints(described, live) {
		switch {
		case strings.HasPrefix(line, "~ "):
			fields := strings.Fields(line[2:])
			if len(fields) > 0 && skip[strings.ToLower(fields[0])] {
				continue
			}
		case strings.HasPrefix(line, "+ module\t"), strings.HasPrefix(line, "- module\t"):
			fields := strings.Split(line[2:], "\t")
			if len(fields) > 1 && skip[strings.ToLower(fields[1])] {
				continue
			}
		}
		out = append(out, line)
	}
	return filterAdditions(out)
}

// recordFingerprint stores the current schema fingerprint. Called inside the
// release transaction so the recorded state always matches what was applied.
func (s *store) recordFingerprint(ctx context.Context, db interface {
	reader
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	if s.noFingerprint {
		return nil
	}
	text, err := fingerprint(ctx, db, s.dialect.name)
	if err != nil {
		return fmt.Errorf("compute schema fingerprint: %w", err)
	}
	if _, err := db.ExecContext(ctx, s.dialect.initializeMeta); err != nil {
		return fmt.Errorf("initialize meta table: %w", err)
	}
	if _, err := db.ExecContext(ctx, s.dialect.upsertMeta, s.dialect.metaArgs(schemaFingerprintKey, text)...); err != nil {
		return fmt.Errorf("store schema fingerprint: %w", err)
	}
	return nil
}

// moduleDefinitionMatches compares a live module definition with the pinned
// file. Catalog definitions that lack a CREATE header (pg_get_viewdef) are
// wrapped into one so both sides have the same shape.
func moduleDefinitionMatches(kind, schema, name, definition, fileSQL string) bool {
	got := NormalizeDefinition(definition)
	if !strings.HasPrefix(got, "create") {
		got = NormalizeDefinition("create or replace " + strings.ToLower(kind) + " " + schema + "." + name + " as " + definition)
	}
	return got == NormalizeDefinition(fileSQL)
}

// Drift compares the live database against the given managed object files and
// the schema fingerprint recorded at apply time.
func (s *store) Drift(ctx context.Context, files []File) (DriftReport, error) {
	current, err := fingerprint(ctx, s.db, s.dialect.name)
	if err != nil {
		return DriftReport{}, err
	}
	var stored sql.NullString
	err = s.db.QueryRowContext(ctx, s.dialect.selectMeta, s.dialect.selectMetaArgs(schemaFingerprintKey)...).Scan(&stored)
	if err != nil && err != sql.ErrNoRows {
		return DriftReport{}, err
	}
	report := DriftReport{SchemaStored: stored.String, SchemaCurrent: current, Objects: []Status{}}
	if stored.Valid && stored.String != current {
		report.SchemaDiff = DiffFingerprints(stored.String, current)
	}
	for _, file := range files {
		if file.Delete {
			continue
		}
		id, err := objectIdentity(file.SQL)
		if err != nil {
			return DriftReport{}, fmt.Errorf("%s: %w", file.Path, err)
		}
		var rows *sql.Rows
		if s.dialect.name == dialect.Postgres {
			if _, err := s.db.ExecContext(ctx, "SET search_path TO pg_catalog"); err != nil {
				return DriftReport{}, err
			}
			rows, err = s.db.QueryContext(ctx, s.dialect.selectModuleDefs, id.schema, id.name)
		} else {
			rows, err = s.db.QueryContext(ctx, s.dialect.selectModuleDefs, sql.Named("schema", id.schema), sql.Named("name", id.name))
		}
		if err != nil {
			return DriftReport{}, err
		}
		state := "missing"
		for rows.Next() {
			var definition string
			if err := rows.Scan(&definition); err != nil {
				rows.Close()
				return DriftReport{}, err
			}
			if moduleDefinitionMatches(id.kind, id.schema, id.name, definition, file.SQL) {
				state = "ok"
				break
			}
			state = "drifted"
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return DriftReport{}, err
		}
		rows.Close()
		report.Objects = append(report.Objects, Status{Path: file.Path, State: state, Checksum: file.Checksum})
	}
	sort.Slice(report.Objects, func(i, j int) bool { return report.Objects[i].Path < report.Objects[j].Path })
	return report, nil
}
